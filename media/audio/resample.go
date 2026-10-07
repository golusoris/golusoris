// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package audio

import (
	"fmt"
	"math"
)

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
func (s *stereoResampler) process(in []float32, ch int, maxOutputFrames int64) ([]float32, error) {
	frames := len(in) / ch
	if frames == 0 {
		return nil, nil
	}
	left, right := toStereo(in, ch, frames)
	return s.resample(left, right, maxOutputFrames)
}

// flush holds the trailing source frame for every remaining output position.
func (s *stereoResampler) flush(maxOutputFrames int64) ([]float32, error) {
	if !s.havePrev {
		return nil, nil
	}
	out := make([]float32, 0, resampleCapacity(1, s.ratio, maxOutputFrames))
	for int64(len(out)/2) < maxOutputFrames && s.pos < 0 {
		out = append(out, s.prevL, s.prevR)
		s.pos += s.ratio
	}
	if s.pos < 0 {
		return nil, fmt.Errorf("audio: resample: %w", ErrInputTooLarge)
	}
	s.havePrev = false
	return out, nil
}

// resample linearly interpolates the per-call stereo frames onto the 48k grid,
// using the trailing frame carried from the previous call at index -1.
func (s *stereoResampler) resample(left, right []float32, maxOutputFrames int64) ([]float32, error) {
	if s.srcRate == s.dstRate {
		if int64(len(left)) > maxOutputFrames {
			return nil, fmt.Errorf("audio: resample: %w", ErrInputTooLarge)
		}
		return interleave(left, right), nil
	}
	srcLen := len(left)
	out := make([]float32, 0, resampleCapacity(srcLen, s.ratio, maxOutputFrames))
	for int64(len(out)/2) < maxOutputFrames {
		leftSample, rightSample, ok := s.sampleAtPosition(left, right)
		if !ok {
			break
		}
		out = append(out, leftSample, rightSample)
		s.pos += s.ratio
	}
	if _, _, ok := s.sampleAtPosition(left, right); ok {
		return nil, fmt.Errorf("audio: resample: %w", ErrInputTooLarge)
	}
	s.pos -= float64(srcLen)
	s.prevL, s.prevR, s.havePrev = left[srcLen-1], right[srcLen-1], true
	return out, nil
}

func resampleCapacity(srcLen int, ratio float64, maxOutputFrames int64) int {
	frames := min(int64(float64(srcLen)/ratio)+2, maxOutputFrames)
	maxInt := int64(^uint(0) >> 1)
	if frames > maxInt/2 {
		frames = maxInt / 2
	}
	if frames < 0 {
		frames = 0
	}
	return int(frames * 2)
}

// sampleAtPosition interpolates the next source position when both endpoints
// are available. A negative position uses the carried frame at index -1.
func (s *stereoResampler) sampleAtPosition(left, right []float32) (float32, float32, bool) {
	base := int(math.Floor(s.pos))
	fraction := float32(s.pos - float64(base))
	if base == -1 {
		if !s.havePrev {
			return 0, 0, false
		}
		return interpolateStereo(s.prevL, s.prevR, left[0], right[0], fraction)
	}
	if base < 0 || base >= len(left) {
		return 0, 0, false
	}
	left0, right0 := left[base], right[base]
	if fraction == 0 {
		return left0, right0, true
	}
	if base+1 >= len(left) {
		return 0, 0, false
	}
	return interpolateStereo(left0, right0, left[base+1], right[base+1], fraction)
}

func interpolateStereo(left0, right0, left1, right1, fraction float32) (float32, float32, bool) {
	return left0 + (left1-left0)*fraction, right0 + (right1-right0)*fraction, true
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
