// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package audio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"time"

	"github.com/exaring/ebur128"
)

// decodeChunkFrames is the streaming read granularity (inter-channel frames).
const decodeChunkFrames = 8192

// ctxCheckMask bounds how often loops poll ctx.Err() (every 64 chunks).
const ctxCheckMask = 0x3F

// decodeChunkNoLimitGuard is the decode-chunk ceiling when no byte budget is
// configured (maxFrames < 0). It is generous but finite: real callers always
// go through [Options.withDefaults], which normalizes MaxDecodedBytes to a
// positive value, so this branch is a defensive fallback rather than an
// expected path.
const decodeChunkNoLimitGuard = 1 << 32

// decodeChunkLimit bounds the decode read loop in streamMono/loudness
// (HISS-02). A well-behaved decoder either advances frameIdx or reports
// EOF/error on every read, so exhausting the maxFrames byte budget takes at
// most maxFrames+1 chunks in the pathological case of one frame per read;
// this also catches a decoder stuck returning zero frames without EOF, which
// would otherwise spin the loop forever.
func decodeChunkLimit(maxFrames int64) int64 {
	if maxFrames < 0 {
		return decodeChunkNoLimitGuard
	}
	if maxFrames == int64(^uint64(0)>>1) {
		return maxFrames
	}
	return maxFrames + 1
}

// waveform mono-mixes the stream and reduces it to buckets min/max peaks.
func (a *analyzer) waveform(ctx context.Context, st pcmStream, buckets int) (PeakSet, error) {
	info := st.info()
	ch := info.Channels
	if ch < 1 {
		return PeakSet{}, fmt.Errorf("audio: waveform: %w: zero channels", ErrCorrupt)
	}
	totalFrames := framesFor(info)
	var acc peakCollector
	if totalFrames > 0 {
		acc = newPeakAccumulator(buckets, totalFrames)
	} else {
		acc = newStreamingPeakAccumulator(buckets)
	}

	ps := PeakSet{
		SampleRate: info.SampleRate,
		Channels:   ch,
		Buckets:    buckets,
		Min:        make([]float32, buckets),
		Max:        make([]float32, buckets),
	}
	read, err := decodeBuffer(ch, a.opts.MaxDecodedBytes)
	if err != nil {
		return PeakSet{}, err
	}

	maxFrames := a.opts.MaxDecodedBytes / bytesPerFrame(ch)
	if err := a.streamMono(ctx, st, read, ch, maxFrames, acc.add); err != nil {
		return PeakSet{}, err
	}
	acc.finish(&ps)
	return ps, nil
}

// streamMono decodes the stream, mono-mixes each frame, and calls fn(frameIdx, mono).
func (a *analyzer) streamMono(
	ctx context.Context,
	st pcmStream,
	read []float32,
	ch int,
	maxFrames int64,
	fn func(frameIdx int64, mono float32),
) error {
	return streamPCM(ctx, "waveform", st, read, ch, maxFrames, func(samples []float32, frames int, firstFrame int64) error {
		for f := range frames {
			fn(firstFrame+int64(f), monoMix(samples[f*ch:f*ch+ch]))
		}
		return nil
	})
}

// loudness resamples the stream to 48k stereo and runs EBU R128.
func (a *analyzer) loudness(ctx context.Context, st pcmStream) (Loudness, error) {
	info := st.info()
	ch := info.Channels
	if ch < 1 {
		return Loudness{}, fmt.Errorf("audio: loudness: %w: zero channels", ErrCorrupt)
	}
	meter, err := ebur128.New(ebur128.LayoutStereo, targetRate)
	if err != nil {
		return Loudness{}, fmt.Errorf("audio: loudness: new meter: %w", err)
	}
	rs := newStereoResampler(info.SampleRate)
	read, err := loudnessDecodeBuffer(ch, info.SampleRate, a.opts.MaxDecodedBytes)
	if err != nil {
		return Loudness{}, err
	}
	maxFrames := a.opts.MaxDecodedBytes / bytesPerFrame(ch)
	maxOutputFrames := a.opts.MaxDecodedBytes / bytesPerFrame(2)
	var outputFrames int64

	err = streamPCM(ctx, "loudness", st, read, ch, maxFrames, func(samples []float32, _ int, _ int64) error {
		remaining := maxOutputFrames - outputFrames
		resampled, resampleErr := rs.process(samples, ch, remaining)
		if resampleErr != nil {
			return resampleErr
		}
		meter.WriteFloat32(resampled)
		outputFrames += int64(len(resampled) / 2)
		return nil
	})
	if err != nil {
		return Loudness{}, err
	}
	flushed, err := rs.flush(maxOutputFrames - outputFrames)
	if err != nil {
		return Loudness{}, fmt.Errorf("audio: loudness: %w", err)
	}
	meter.WriteFloat32(flushed)
	meter.Finalize()
	res := meter.Loudness()
	return Loudness{
		IntegratedLUFS:  res.IntegratedLoudness,
		TruePeakDBTP:    res.TruePeak,
		LoudnessRangeLU: res.LoudnessRange,
	}, nil
}

type pcmChunkConsumer func(samples []float32, frames int, firstFrame int64) error

func streamPCM(
	ctx context.Context,
	op string,
	st pcmStream,
	read []float32,
	channels int,
	maxFrames int64,
	consume pcmChunkConsumer,
) error {
	var frameIdx int64
	limit := decodeChunkLimit(maxFrames)
	for chunk := range limit {
		if err := checkDecodeContext(ctx, op, chunk); err != nil {
			return err
		}
		done, err := readPCMChunk(st, read, channels, maxFrames, &frameIdx, consume)
		if err != nil {
			return fmt.Errorf("audio: %s: %w", op, err)
		}
		if done {
			return nil
		}
	}
	return fmt.Errorf("audio: %s: exceeded %d decode chunks without EOF", op, limit)
}

func checkDecodeContext(ctx context.Context, op string, chunk int64) error {
	if chunk&ctxCheckMask != 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("audio: %s: %w", op, err)
	}
	return nil
}

func readPCMChunk(
	st pcmStream,
	read []float32,
	channels int,
	maxFrames int64,
	frameIdx *int64,
	consume pcmChunkConsumer,
) (bool, error) {
	if channels < 1 {
		return false, fmt.Errorf("%w: invalid channel count %d", ErrCorrupt, channels)
	}
	n, readErr := st.read(read)
	if err := validatePCMRead(n, len(read), channels); err != nil {
		return false, err
	}
	if n == 0 && readErr == nil {
		return false, io.ErrNoProgress
	}
	frames := n / channels
	if frames > 0 {
		if exceedsFrameLimit(*frameIdx, int64(frames), maxFrames) {
			return false, ErrInputTooLarge
		}
		if err := consume(read[:frames*channels], frames, *frameIdx); err != nil {
			return false, err
		}
		*frameIdx += int64(frames)
	}
	if readErr == nil {
		return false, nil
	}
	if errors.Is(readErr, io.EOF) {
		return true, nil
	}
	return false, readErr // already wrapped by the decoder
}

func validatePCMRead(samples, bufferSize, channels int) error {
	if samples < 0 || samples > bufferSize || samples%channels != 0 {
		return fmt.Errorf("%w: decoder returned %d samples for %d channels", ErrCorrupt, samples, channels)
	}
	return nil
}

func exceedsFrameLimit(current, next, maximum int64) bool {
	return maximum >= 0 && (current > maximum || next > maximum-current)
}

// monoMix averages a single interleaved frame to one channel.
func monoMix(frame []float32) float32 {
	if len(frame) == 1 {
		return frame[0]
	}
	var sum float32
	for _, s := range frame {
		sum += s
	}
	return sum / float32(len(frame))
}

// framesFor returns the inter-channel frame count from duration/rate, or 0.
func framesFor(info Info) int64 {
	if info.SampleRate <= 0 || info.Duration <= 0 {
		return 0
	}
	high, low := bits.Mul64(uint64(info.Duration), uint64(info.SampleRate))
	divisor := uint64(time.Second)
	if high >= divisor {
		return int64(^uint64(0) >> 1)
	}
	frames, _ := bits.Div64(high, low, divisor)
	if frames > ^uint64(0)>>1 {
		return int64(^uint64(0) >> 1)
	}
	return int64(frames)
}

// bytesPerFrame is the in-memory float32 cost of one inter-channel frame.
func bytesPerFrame(ch int) int64 {
	if ch < 1 {
		ch = 1
	}
	return int64(ch) * 4
}

func decodeBuffer(channels int, maxBytes int64) ([]float32, error) {
	return decodeBufferWithFrameLimit(channels, maxBytes, decodeChunkFrames)
}

func loudnessDecodeBuffer(channels, sampleRate int, maxBytes int64) ([]float32, error) {
	maxFrames := decodeChunkFrames
	if sampleRate < targetRate {
		maxFrames = max(int(int64(decodeChunkFrames)*int64(sampleRate)/targetRate), 1)
	}
	return decodeBufferWithFrameLimit(channels, maxBytes, maxFrames)
}

func decodeBufferWithFrameLimit(channels int, maxBytes int64, maxFrames int) ([]float32, error) {
	frames, err := boundedChunkFrames(channels, maxBytes, 4, maxFrames)
	if err != nil {
		return nil, err
	}
	return make([]float32, frames*channels), nil
}

func boundedChunkFrames(channels int, maxBytes, sampleBytes int64, maxFrames int) (int, error) {
	if channels < 1 || maxBytes <= 0 || sampleBytes <= 0 || maxFrames < 1 {
		return 0, fmt.Errorf("audio: decode buffer: %w", ErrInputTooLarge)
	}
	if int64(channels) > int64(^uint64(0)>>1)/sampleBytes {
		return 0, fmt.Errorf("audio: decode buffer: %w", ErrInputTooLarge)
	}
	frameBytes := int64(channels) * sampleBytes
	frames := maxBytes / frameBytes
	if frames < 1 {
		return 0, fmt.Errorf("audio: decode buffer: %w", ErrInputTooLarge)
	}
	if frames > int64(maxFrames) {
		frames = int64(maxFrames)
	}
	return int(frames), nil
}
