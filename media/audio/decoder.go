package audio

import (
	"bytes"
	"errors"
	"fmt"
	"io"
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
func (a *analyzer) open(r io.Reader, hint Format) (pcmStream, error) {
	head, src, err := sniffHead(r)
	if err != nil {
		return nil, err
	}
	switch sniff(head, hint) {
	case FormatMP3:
		return openMP3(src)
	case FormatOGG:
		return openOGG(src)
	case FormatFLAC:
		return openFLAC(src)
	case FormatWAV:
		return a.openSeekable(src, FormatWAV)
	case FormatAIFF:
		return a.openSeekable(src, FormatAIFF)
	default:
		return nil, fmt.Errorf("audio: %w", ErrUnknownFormat)
	}
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
	case len(head) >= 12 && bytes.Equal(head[0:4], []byte("RIFF")) &&
		bytes.Equal(head[8:12], []byte("WAVE")):
		return FormatWAV
	case len(head) >= 12 && bytes.Equal(head[0:4], []byte("FORM")) &&
		(bytes.Equal(head[8:12], []byte("AIFF")) || bytes.Equal(head[8:12], []byte("AIFC"))):
		return FormatAIFF
	case isMP3Head(head):
		return FormatMP3
	default:
		return hint // inconclusive: trust the caller's hint, or "" => unknown
	}
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
	r         io.Reader
	remaining int64
}

func (b *boundedReader) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		return 0, ErrInputTooLarge
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
