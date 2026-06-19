package audio

// targetRate is the only sample rate EBU R128 (exaring/ebur128) accepts.
const targetRate = 48000

// stereoResampler converts an arbitrary-rate, arbitrary-channel interleaved
// float32 stream into 48 kHz interleaved stereo via linear interpolation.
// It carries one trailing frame across process calls so chunk boundaries do
// not introduce gaps.
type stereoResampler struct {
	srcRate  int
	dstRate  int
	ratio    float64 // srcRate / dstRate (input frames per output frame)
	pos      float64 // fractional source position of the next output frame
	prevL    float32 // last source-left from the previous chunk
	prevR    float32 // last source-right from the previous chunk
	havePrev bool
}

// newStereoResampler builds a resampler from srcRate to the fixed targetRate
// (the only rate EBU R128 accepts).
func newStereoResampler(srcRate int) *stereoResampler {
	if srcRate <= 0 {
		srcRate = targetRate
	}
	return &stereoResampler{
		srcRate: srcRate,
		dstRate: targetRate,
		ratio:   float64(srcRate) / float64(targetRate),
	}
}

// process downmixes one interleaved chunk to stereo and resamples to dstRate.
// in holds frames*ch samples. The returned slice is interleaved stereo at 48k.
func (s *stereoResampler) process(in []float32, ch int) []float32 {
	frames := len(in) / ch
	if frames == 0 {
		return nil
	}
	left, right := toStereo(in, ch, frames)
	return s.resample(left, right)
}

// flush emits the final partial output frame from the carried trailing sample.
func (s *stereoResampler) flush() []float32 {
	if !s.havePrev || s.pos <= 0 {
		return nil
	}
	// One last frame holding the trailing source sample (no successor to lerp).
	s.havePrev = false
	return []float32{s.prevL, s.prevR}
}

// resample linearly interpolates the per-call stereo frames onto the 48k grid,
// using the trailing frame carried from the previous call as index -1.
func (s *stereoResampler) resample(left, right []float32) []float32 {
	if s.srcRate == s.dstRate {
		return interleave(left, right)
	}
	srcLen := len(left)
	out := make([]float32, 0, int(float64(srcLen)/s.ratio)+2)
	// pos is measured in source-frame units within [prev, this-chunk] space,
	// where index 0 is the first sample of this chunk and -1 is the carried one.
	for s.pos < float64(srcLen) {
		idx := s.pos
		i := int(idx)
		frac := float32(idx - float64(i))
		l0, r0 := s.sampleAt(left, right, i-1)
		l1, r1 := s.sampleAt(left, right, i)
		out = append(out, l0+(l1-l0)*frac, r0+(r1-r0)*frac)
		s.pos += s.ratio
	}
	s.pos -= float64(srcLen)
	if srcLen > 0 {
		s.prevL, s.prevR, s.havePrev = left[srcLen-1], right[srcLen-1], true
	}
	return out
}

// sampleAt returns the source frame at i; i == -1 yields the carried frame.
func (s *stereoResampler) sampleAt(left, right []float32, i int) (float32, float32) {
	if i < 0 {
		if s.havePrev {
			return s.prevL, s.prevR
		}
		return left[0], right[0]
	}
	if i >= len(left) {
		i = len(left) - 1
	}
	return left[i], right[i]
}

// toStereo splits an interleaved chunk into left/right channel slices.
func toStereo(in []float32, ch, frames int) ([]float32, []float32) {
	left := make([]float32, frames)
	right := make([]float32, frames)
	switch ch {
	case 1:
		for f := range frames {
			left[f], right[f] = in[f], in[f]
		}
	case 2:
		for f := range frames {
			left[f], right[f] = in[f*2], in[f*2+1]
		}
	default:
		for f := range frames {
			left[f], right[f] = in[f*ch], in[f*ch+1] // L/R from a multichannel layout
		}
	}
	return left, right
}

// interleave zips left/right channel slices back into interleaved stereo.
func interleave(left, right []float32) []float32 {
	out := make([]float32, len(left)*2)
	for i := range left {
		out[i*2] = left[i]
		out[i*2+1] = right[i]
	}
	return out
}
