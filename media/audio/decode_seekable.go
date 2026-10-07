// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package audio

import (
	"bytes"
	"fmt"
	"io"
	"strconv"

	goaiff "github.com/go-audio/aiff"
	goaudio "github.com/go-audio/audio"
	gowav "github.com/go-audio/wav"
)

// pcmBufferFrames is the per-call decode chunk size (inter-channel frames).
const pcmBufferFrames = 8192

// intBufferReader is the common shape of go-audio's wav/aiff decoders.
type intBufferReader interface {
	PCMBuffer(buf *goaudio.IntBuffer) (int, error)
}

// seekableStream adapts a go-audio int-PCM decoder (wav/aiff) to pcmStream.
type seekableStream struct {
	dec      intBufferReader
	intBuf   *goaudio.IntBuffer
	pending  []float32
	bitDepth int
	meta     Info
	maxBytes int64
}

// openSeekable preserves seekable inputs and buffers only readers that the
// go-audio decoders cannot seek themselves.
func (a *analyzer) openSeekable(r io.Reader, format Format) (pcmStream, error) {
	rs, ok := r.(io.ReadSeeker)
	if !ok {
		raw, err := io.ReadAll(r)
		if err != nil {
			return nil, fmt.Errorf("audio: buffer input: %w", err)
		}
		rs = bytes.NewReader(raw)
	}
	switch format {
	case FormatWAV:
		return newWAVStream(rs, a.opts)
	case FormatAIFF:
		return newAIFFStream(rs, a.opts)
	case FormatMP3, FormatOGG, FormatFLAC: // streaming formats: not seekable-decoded
		return nil, fmt.Errorf("audio: %w", ErrUnknownFormat)
	default:
		return nil, fmt.Errorf("audio: %w", ErrUnknownFormat)
	}
}

func newWAVStream(rs io.ReadSeeker, opts Options) (pcmStream, error) {
	dec := gowav.NewDecoder(rs)
	dec.ReadInfo()
	if !dec.IsValidFile() {
		return nil, fmt.Errorf("audio: wav: %w", ErrCorrupt)
	}
	dur, err := dec.Duration()
	if err != nil {
		return nil, fmt.Errorf("audio: wav duration: %w: %w", ErrCorrupt, err)
	}
	info := Info{
		Format:     FormatWAV,
		Duration:   dur,
		SampleRate: int(dec.SampleRate),
		Channels:   int(dec.NumChans),
		BitDepth:   int(dec.BitDepth),
		BitRate:    int64(dec.AvgBytesPerSec) * 8,
	}
	if err = validateAudioInfo(info, opts.MaxChannels); err != nil {
		return nil, err
	}
	return newSeekableStream(dec, info, opts.MaxDecodedBytes), nil
}

func newAIFFStream(rs io.ReadSeeker, opts Options) (pcmStream, error) {
	dec := goaiff.NewDecoder(rs)
	dec.ReadInfo()
	if !dec.IsValidFile() {
		return nil, fmt.Errorf("audio: aiff: %w", ErrCorrupt)
	}
	dur, err := dec.Duration()
	if err != nil {
		return nil, fmt.Errorf("audio: aiff duration: %w: %w", ErrCorrupt, err)
	}
	info := Info{
		Format:     FormatAIFF,
		Duration:   dur,
		SampleRate: dec.SampleRate,
		Channels:   int(dec.NumChans),
		BitDepth:   int(dec.BitDepth),
	}
	if err = validateAudioInfo(info, opts.MaxChannels); err != nil {
		return nil, err
	}
	return newSeekableStream(dec, info, opts.MaxDecodedBytes), nil
}

func newSeekableStream(dec intBufferReader, info Info, maxBytes int64) pcmStream {
	bd := info.BitDepth
	if bd <= 0 {
		bd = 16
	}
	return &seekableStream{
		dec:      dec,
		bitDepth: bd,
		meta:     info,
		maxBytes: maxBytes,
	}
}

func (s *seekableStream) info() Info { return s.meta }

func (s *seekableStream) read(out []float32) (int, error) {
	for len(s.pending) < len(out) {
		more, err := s.decodeChunk()
		if err != nil {
			return 0, err
		}
		if len(more) == 0 {
			break
		}
		s.pending = append(s.pending, more...)
	}
	n := copy(out, s.pending)
	s.pending = s.pending[n:]
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

// decodeChunk pulls one IntBuffer chunk and converts it to float32 PCM.
func (s *seekableStream) decodeChunk() ([]float32, error) {
	if err := s.ensureBuffer(); err != nil {
		return nil, err
	}
	n, err := s.dec.PCMBuffer(s.intBuf)
	if err != nil {
		return nil, fmt.Errorf("audio: pcm decode: %w: %w", ErrCorrupt, err)
	}
	if n < 0 || n > len(s.intBuf.Data) {
		return nil, fmt.Errorf("audio: pcm decode: %w: decoder returned %d samples", ErrCorrupt, n)
	}
	if n == 0 {
		return nil, nil
	}
	out := make([]float32, n)
	for i := range n {
		out[i] = seekableSampleToFloat(s.intBuf.Data[i], s.bitDepth)
	}
	return out, nil
}

// seekableSampleToFloat handles go-audio's unsigned representation for
// eight-bit WAV and AIFF samples; wider PCM samples are signed.
func seekableSampleToFloat(sample int, bitDepth int) float32 {
	if bitDepth == 8 {
		return clampUnit(float32(sample-128) / 128)
	}
	return intSampleToFloat(sample, bitDepth)
}

func (s *seekableStream) ensureBuffer() error {
	if s.intBuf != nil {
		return nil
	}
	frames, err := boundedChunkFrames(s.meta.Channels, s.maxBytes, int64(strconv.IntSize/8), pcmBufferFrames)
	if err != nil {
		return err
	}
	s.intBuf = &goaudio.IntBuffer{
		Format: &goaudio.Format{NumChannels: s.meta.Channels, SampleRate: s.meta.SampleRate},
		Data:   make([]int, frames*s.meta.Channels),
	}
	return nil
}
