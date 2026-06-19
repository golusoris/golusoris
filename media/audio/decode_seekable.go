package audio

import (
	"bytes"
	"fmt"
	"io"

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
}

// openSeekable buffers the (bounded) input into memory so the go-audio decoders
// get the io.ReadSeeker they require, then reads header metadata.
func (a *analyzer) openSeekable(r io.Reader, format Format) (pcmStream, error) {
	raw, err := io.ReadAll(&boundedReader{r: r, remaining: a.opts.MaxDecodedBytes})
	if err != nil {
		return nil, fmt.Errorf("audio: buffer input: %w", err)
	}
	rs := bytes.NewReader(raw)
	switch format {
	case FormatWAV:
		return newWAVStream(rs)
	case FormatAIFF:
		return newAIFFStream(rs)
	case FormatMP3, FormatOGG, FormatFLAC: // streaming formats: not seekable-decoded
		return nil, fmt.Errorf("audio: %w", ErrUnknownFormat)
	default:
		return nil, fmt.Errorf("audio: %w", ErrUnknownFormat)
	}
}

func newWAVStream(rs io.ReadSeeker) (pcmStream, error) {
	dec := gowav.NewDecoder(rs)
	dec.ReadInfo()
	if !dec.IsValidFile() {
		return nil, fmt.Errorf("audio: wav: %w", ErrCorrupt)
	}
	dur, err := dec.Duration()
	if err != nil {
		return nil, fmt.Errorf("audio: wav duration: %w: %w", ErrCorrupt, err)
	}
	return newSeekableStream(dec, Info{
		Format:     FormatWAV,
		Duration:   dur,
		SampleRate: int(dec.SampleRate),
		Channels:   int(dec.NumChans),
		BitDepth:   int(dec.BitDepth),
		BitRate:    int64(dec.AvgBytesPerSec) * 8,
	}), nil
}

func newAIFFStream(rs io.ReadSeeker) (pcmStream, error) {
	dec := goaiff.NewDecoder(rs)
	dec.ReadInfo()
	if !dec.IsValidFile() {
		return nil, fmt.Errorf("audio: aiff: %w", ErrCorrupt)
	}
	dur, err := dec.Duration()
	if err != nil {
		return nil, fmt.Errorf("audio: aiff duration: %w: %w", ErrCorrupt, err)
	}
	return newSeekableStream(dec, Info{
		Format:     FormatAIFF,
		Duration:   dur,
		SampleRate: dec.SampleRate,
		Channels:   int(dec.NumChans),
		BitDepth:   int(dec.BitDepth),
	}), nil
}

func newSeekableStream(dec intBufferReader, info Info) pcmStream {
	bd := info.BitDepth
	if bd <= 0 {
		bd = 16
	}
	return &seekableStream{
		dec: dec,
		intBuf: &goaudio.IntBuffer{
			Format: &goaudio.Format{NumChannels: info.Channels, SampleRate: info.SampleRate},
			Data:   make([]int, pcmBufferFrames*maxChannels(info.Channels)),
		},
		bitDepth: bd,
		meta:     info,
	}
}

// maxChannels guards the scratch-buffer size against a zero/garbage channel count.
func maxChannels(ch int) int {
	if ch < 1 {
		return 1
	}
	return ch
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
	n, err := s.dec.PCMBuffer(s.intBuf)
	if err != nil {
		return nil, fmt.Errorf("audio: pcm decode: %w: %w", ErrCorrupt, err)
	}
	if n == 0 {
		return nil, nil
	}
	out := make([]float32, n)
	for i := range n {
		out[i] = intSampleToFloat(s.intBuf.Data[i], s.bitDepth)
	}
	return out, nil
}
