// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package audio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"time"
)

// sniffLen is how many header bytes are read to identify the format.
const sniffLen = 12

// pcmStream is the internal SPI: a decoded, interleaved float32 PCM source in
// the range [-1, 1] plus its header metadata. read returns io.EOF when drained.
type pcmStream interface {
	info() Info
	read(buf []float32) (n int, err error)
}

// open sniffs the header, picks a decoder, and returns a streaming pcmStream.
// The magic-byte sniff takes precedence over hint; hint only disambiguates
// when the sniff is inconclusive (e.g. headerless MP3 frame data).
func (a *analyzer) open(ctx context.Context, r io.Reader, hint Format) (pcmStream, error) {
	source, err := boundInput(ctx, r, a.opts.MaxInputBytes)
	if err != nil {
		return nil, err
	}
	head, src, err := sniffHead(source)
	if err != nil {
		return nil, err
	}
	var stream pcmStream
	switch sniff(head, hint) {
	case FormatMP3:
		stream, err = openMP3(src)
	case FormatOGG:
		stream, err = openOGG(src)
	case FormatFLAC:
		stream, err = openFLAC(src)
	case FormatWAV:
		stream, err = a.openSeekable(src, FormatWAV)
	case FormatAIFF:
		stream, err = a.openSeekable(src, FormatAIFF)
	default:
		return nil, fmt.Errorf("audio: %w", ErrUnknownFormat)
	}
	if err != nil {
		return nil, err
	}
	if err = validateAudioInfo(stream.info(), a.opts.MaxChannels); err != nil {
		return nil, err
	}
	return stream, nil
}

func validateAudioInfo(info Info, maxChannels int) error {
	if info.Channels < 1 || info.Channels > maxChannels {
		return fmt.Errorf("audio: metadata: %w: channels %d outside 1..%d", ErrCorrupt, info.Channels, maxChannels)
	}
	if info.SampleRate <= 0 {
		return fmt.Errorf("audio: metadata: %w: invalid sample rate %d", ErrCorrupt, info.SampleRate)
	}
	if info.BitDepth < 0 || info.BitDepth > 64 {
		return fmt.Errorf("audio: metadata: %w: invalid bit depth %d", ErrCorrupt, info.BitDepth)
	}
	return nil
}

// sniffHead reads up to sniffLen header bytes and returns a reader positioned at
// the original start. A seekable source is rewound so the decoder can use its
// fast length probe; a non-seekable source is reconstructed with a MultiReader.
func sniffHead(r io.Reader) ([]byte, io.Reader, error) {
	head := make([]byte, sniffLen)
	n, err := io.ReadFull(r, head)
	head = head[:n]
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, nil, fmt.Errorf("audio: read header: %w", err)
	}
	if n == 0 {
		return nil, nil, fmt.Errorf("audio: %w: empty input", ErrUnknownFormat)
	}
	if rs, ok := r.(io.Seeker); ok {
		if _, serr := rs.Seek(0, io.SeekStart); serr == nil {
			return head, r, nil
		} else if isInputBoundaryError(serr) {
			return nil, nil, fmt.Errorf("audio: rewind input: %w", serr)
		}
	}
	return head, io.MultiReader(bytes.NewReader(head), r), nil
}

// sniff identifies a format from header bytes, falling back to hint only when
// the magic bytes are inconclusive.
func sniff(head []byte, hint Format) Format {
	switch {
	case bytes.HasPrefix(head, []byte("OggS")):
		return FormatOGG
	case bytes.HasPrefix(head, []byte("fLaC")):
		return FormatFLAC
	case isWAVHead(head):
		return FormatWAV
	case isAIFFHead(head):
		return FormatAIFF
	case isMP3Head(head):
		return FormatMP3
	default:
		return hint // inconclusive: trust the caller's hint, or "" => unknown
	}
}

func isWAVHead(head []byte) bool {
	return len(head) >= 12 && bytes.Equal(head[0:4], []byte("RIFF")) &&
		bytes.Equal(head[8:12], []byte("WAVE"))
}

func isAIFFHead(head []byte) bool {
	return len(head) >= 12 && bytes.Equal(head[0:4], []byte("FORM")) &&
		(bytes.Equal(head[8:12], []byte("AIFF")) || bytes.Equal(head[8:12], []byte("AIFC")))
}

// isMP3Head reports whether head starts with an ID3 tag or an MPEG frame sync.
func isMP3Head(head []byte) bool {
	if bytes.HasPrefix(head, []byte("ID3")) {
		return true
	}
	// MPEG audio frame sync: 11 set bits (0xFF 0xEx). Layer III lives here too.
	return len(head) >= 2 && head[0] == 0xFF && (head[1]&0xE0) == 0xE0
}

// boundedReader caps how many bytes a decoder may pull, converting the cap to
// ErrInputTooLarge instead of a generic decode failure.
type boundedReader struct {
	check     func() error
	r         io.Reader
	remaining int64
}

type boundedReadSeeker struct {
	check    func() error
	source   io.ReadSeeker
	limit    int64
	position int64
}

func newBoundedReadSeeker(
	ctx context.Context,
	source io.ReadSeeker,
	limit int64,
) (*boundedReadSeeker, error) {
	if err := contextError(ctx); err != nil {
		return nil, fmt.Errorf("audio: inspect input: %w", err)
	}
	position, err := source.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, fmt.Errorf("audio: inspect input position: %w", err)
	}
	if position < 0 || position > limit {
		return nil, fmt.Errorf(
			"audio: input position %d exceeds limit %d: %w",
			position,
			limit,
			ErrInputTooLarge,
		)
	}
	end, endErr := source.Seek(0, io.SeekEnd)
	if _, err = source.Seek(position, io.SeekStart); err != nil {
		return nil, fmt.Errorf("audio: restore input position: %w", err)
	}
	if endErr == nil && end > limit {
		return nil, fmt.Errorf("audio: input size %d exceeds limit %d: %w", end, limit, ErrInputTooLarge)
	}
	return &boundedReadSeeker{
		check:    contextCheck(ctx),
		source:   source,
		limit:    limit,
		position: position,
	}, nil
}

func (b *boundedReadSeeker) Read(p []byte) (int, error) {
	reader := boundedReader{check: b.check, r: b.source, remaining: b.limit - b.position}
	n, err := reader.Read(p)
	b.position += int64(n)
	return n, err
}

func (b *boundedReadSeeker) Seek(offset int64, whence int) (int64, error) {
	if err := runContextCheck(b.check); err != nil {
		return 0, fmt.Errorf("audio: seek input: %w", err)
	}
	position, err := b.source.Seek(offset, whence)
	if err != nil {
		return 0, fmt.Errorf("audio: seek input: %w", err)
	}
	if position < 0 || position > b.limit {
		if _, restoreErr := b.source.Seek(b.position, io.SeekStart); restoreErr != nil {
			return position, fmt.Errorf("audio: restore input after rejected seek: %w", restoreErr)
		}
		return position, fmt.Errorf("audio: seek position %d exceeds limit %d: %w", position, b.limit, ErrInputTooLarge)
	}
	b.position = position
	return position, nil
}

func (b *boundedReader) Read(p []byte) (int, error) {
	if err := runContextCheck(b.check); err != nil {
		return 0, fmt.Errorf("audio: read input: %w", err)
	}
	if len(p) == 0 {
		return 0, nil
	}
	if b.remaining <= 0 {
		var sentinel [1]byte
		n, err := b.r.Read(sentinel[:])
		if n > 0 {
			return 0, ErrInputTooLarge
		}
		if errors.Is(err, io.EOF) {
			return 0, io.EOF
		}
		if err != nil {
			return 0, fmt.Errorf("audio: read input sentinel: %w", err)
		}
		return 0, fmt.Errorf("audio: read input sentinel: %w", io.ErrNoProgress)
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.r.Read(p)
	b.remaining -= int64(n)
	// io.EOF must propagate verbatim so io.ReadAll terminates normally.
	if err != nil && !errors.Is(err, io.EOF) {
		return n, fmt.Errorf("audio: read input: %w", err)
	}
	return n, err //nolint:wrapcheck // EOF forwarded verbatim for io.ReadAll
}

func boundInput(ctx context.Context, r io.Reader, maxBytes int64) (io.Reader, error) {
	if maxBytes <= 0 {
		return nil, fmt.Errorf("audio: bound input: %w", ErrInputTooLarge)
	}
	if seeker, ok := r.(io.ReadSeeker); ok {
		return newBoundedReadSeeker(ctx, seeker, maxBytes)
	}
	if err := contextError(ctx); err != nil {
		return nil, fmt.Errorf("audio: bound input: %w", err)
	}
	return &boundedReader{check: contextCheck(ctx), r: r, remaining: maxBytes}, nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func contextCheck(ctx context.Context) func() error {
	if ctx == nil {
		return nil
	}
	return ctx.Err
}

func runContextCheck(check func() error) error {
	if check == nil {
		return nil
	}
	return check()
}

func isInputBoundaryError(err error) bool {
	return errors.Is(err, ErrInputTooLarge) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}

func durationForFrames(frames int64, sampleRate int) time.Duration {
	if frames <= 0 {
		return 0
	}
	return durationForUnsignedFrames(uint64(frames), sampleRate)
}

func durationForUnsignedFrames(frames uint64, sampleRate int) time.Duration {
	if frames == 0 || sampleRate <= 0 {
		return 0
	}
	const maxDuration = time.Duration(1<<63 - 1)
	rate := uint64(sampleRate)
	seconds, remainder := frames/rate, frames%rate
	maxNanos := uint64(maxDuration)
	if seconds > maxNanos/uint64(time.Second) {
		return maxDuration
	}
	nanos := seconds * uint64(time.Second)
	high, low := bits.Mul64(remainder, uint64(time.Second))
	fraction, _ := bits.Div64(high, low, rate)
	if fraction > maxNanos-nanos {
		return maxDuration
	}
	// #nosec G115 -- the sum is proven no greater than maxDuration above.
	return time.Duration(nanos + fraction)
}

// int16ToFloat converts a signed 16-bit sample to float32 in [-1, 1].
func int16ToFloat(s int16) float32 {
	return float32(s) / 32768.0
}

// intSampleToFloat converts an integer PCM sample of bitDepth bits to [-1, 1].
func intSampleToFloat(s int, bitDepth int) float32 {
	if bitDepth <= 1 {
		return 0
	}
	full := float32(int64(1) << (bitDepth - 1))
	v := float32(s) / full
	return clampUnit(v)
}

// clampUnit clamps v into [-1, 1].
func clampUnit(v float32) float32 {
	if v > 1 {
		return 1
	}
	if v < -1 {
		return -1
	}
	return v
}
