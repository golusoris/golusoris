package audio

import (
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	gomp3 "github.com/hajimehoshi/go-mp3"
	"github.com/jfreymuth/oggvorbis"
	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"
)

// --- MP3 -------------------------------------------------------------------

// mp3Stream adapts go-mp3 (16-bit LE stereo) to pcmStream.
type mp3Stream struct {
	dec  *gomp3.Decoder
	buf  []byte // scratch for raw int16 bytes
	meta Info
}

func openMP3(r io.Reader) (pcmStream, error) {
	dec, err := gomp3.NewDecoder(r)
	if err != nil {
		return nil, fmt.Errorf("audio: mp3: %w: %w", ErrCorrupt, err)
	}
	// go-mp3 always yields 16-bit stereo; Length is bytes (4 per frame).
	rate := dec.SampleRate()
	frames := dec.Length() / 4
	var dur time.Duration
	if rate > 0 {
		dur = time.Duration(frames) * time.Second / time.Duration(rate)
	}
	return &mp3Stream{
		dec: dec,
		meta: Info{
			Format:     FormatMP3,
			Duration:   dur,
			SampleRate: rate,
			Channels:   2,
			BitDepth:   0,
		},
	}, nil
}

func (s *mp3Stream) info() Info { return s.meta }

func (s *mp3Stream) read(out []float32) (int, error) {
	need := len(out) * 2 // 2 bytes per sample
	if cap(s.buf) < need {
		s.buf = make([]byte, need)
	}
	b := s.buf[:need]
	n, err := io.ReadFull(s.dec, b)
	got := n / 2
	for i := range got {
		lo := uint16(b[i*2])
		hi := uint16(b[i*2+1])
		//nolint:gosec // G115: intentional LE int16 PCM bit-reinterpret; full uint16 range maps to int16.
		out[i] = int16ToFloat(int16(lo | hi<<8))
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		if got > 0 {
			return got, nil
		}
		return 0, io.EOF
	}
	if err != nil {
		return got, fmt.Errorf("audio: mp3 decode: %w: %w", ErrCorrupt, err)
	}
	return got, nil
}

// --- OGG/Vorbis ------------------------------------------------------------

// oggStream adapts jfreymuth/oggvorbis (native float32) to pcmStream.
type oggStream struct {
	r    *oggvorbis.Reader
	meta Info
}

func openOGG(r io.Reader) (pcmStream, error) {
	rd, err := oggvorbis.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("audio: ogg: %w: %w", ErrCorrupt, err)
	}
	rate := rd.SampleRate()
	ch := rd.Channels()
	var dur time.Duration
	if rate > 0 {
		dur = time.Duration(rd.Length()) * time.Second / time.Duration(rate)
	}
	return &oggStream{
		r: rd,
		meta: Info{
			Format:     FormatOGG,
			Duration:   dur,
			SampleRate: rate,
			Channels:   ch,
			BitDepth:   0,
		},
	}, nil
}

func (s *oggStream) info() Info { return s.meta }

func (s *oggStream) read(out []float32) (int, error) {
	n, err := s.r.Read(out)
	for i := range n {
		out[i] = clampUnit(out[i])
	}
	if errors.Is(err, io.EOF) {
		if n > 0 {
			return n, nil
		}
		return 0, io.EOF
	}
	if err != nil {
		return n, fmt.Errorf("audio: ogg decode: %w: %w", ErrCorrupt, err)
	}
	return n, nil
}

// --- FLAC ------------------------------------------------------------------

// flacStream adapts mewkiz/flac (per-channel int32 subframes) to pcmStream.
type flacStream struct {
	stream   *flac.Stream
	meta     Info
	pending  []float32 // leftover interleaved samples from the last frame
	bitDepth int
}

func openFLAC(r io.Reader) (pcmStream, error) {
	st, err := flac.New(r)
	if err != nil {
		return nil, fmt.Errorf("audio: flac: %w: %w", ErrCorrupt, err)
	}
	si := st.Info
	rate := int(si.SampleRate)
	ch := int(si.NChannels)
	var dur time.Duration
	if rate > 0 && si.NSamples > 0 && si.NSamples <= math.MaxInt64 {
		dur = time.Duration(si.NSamples) * time.Second / time.Duration(rate)
	}
	return &flacStream{
		stream: st,
		meta: Info{
			Format:     FormatFLAC,
			Duration:   dur,
			SampleRate: rate,
			Channels:   ch,
			BitDepth:   int(si.BitsPerSample),
		},
		bitDepth: int(si.BitsPerSample),
	}, nil
}

func (s *flacStream) info() Info { return s.meta }

func (s *flacStream) read(out []float32) (int, error) {
	for len(s.pending) < len(out) {
		more, err := s.nextFrame()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return 0, err
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

// nextFrame parses one FLAC frame and interleaves its subframes to float32.
func (s *flacStream) nextFrame() ([]float32, error) {
	fr, err := s.stream.ParseNext()
	if errors.Is(err, io.EOF) {
		return nil, io.EOF
	}
	if err != nil {
		return nil, fmt.Errorf("audio: flac frame: %w: %w", ErrCorrupt, err)
	}
	return interleaveFLAC(fr, s.bitDepth), nil
}

// interleaveFLAC interleaves per-channel int32 subframes into float32 PCM.
func interleaveFLAC(fr *frame.Frame, bitDepth int) []float32 {
	ch := len(fr.Subframes)
	if ch == 0 {
		return nil
	}
	nSamples := fr.Subframes[0].NSamples
	out := make([]float32, 0, nSamples*ch)
	for i := range nSamples {
		for c := range ch {
			out = append(out, intSampleToFloat(int(fr.Subframes[c].Samples[i]), bitDepth))
		}
	}
	return out
}
