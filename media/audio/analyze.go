package audio

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/exaring/ebur128"
)

// decodeChunkFrames is the streaming read granularity (inter-channel frames).
const decodeChunkFrames = 8192

// ctxCheckMask bounds how often loops poll ctx.Err() (every 64 chunks).
const ctxCheckMask = 0x3F

// waveform mono-mixes the stream and reduces it to buckets min/max peaks.
func (a *analyzer) waveform(ctx context.Context, st pcmStream, buckets int) (PeakSet, error) {
	info := st.info()
	ch := info.Channels
	if ch < 1 {
		return PeakSet{}, fmt.Errorf("audio: waveform: %w: zero channels", ErrCorrupt)
	}
	totalFrames := framesFor(info)
	if totalFrames <= 0 {
		totalFrames = int64(buckets) // unknown length: degrade to per-read bucketing
	}

	ps := PeakSet{
		SampleRate: info.SampleRate,
		Channels:   ch,
		Buckets:    buckets,
		Min:        make([]float32, buckets),
		Max:        make([]float32, buckets),
	}
	acc := newPeakAccumulator(buckets, totalFrames)
	read := make([]float32, decodeChunkFrames*ch)

	maxFrames := a.opts.MaxDecodedBytes / int64(bytesPerFrame(ch))
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
	var frameIdx int64
	for chunk := 0; ; chunk++ {
		if chunk&ctxCheckMask == 0 {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("audio: waveform: %w", err)
			}
		}
		n, err := st.read(read)
		frames := n / ch
		for f := range frames {
			fn(frameIdx, monoMix(read[f*ch:f*ch+ch]))
			frameIdx++
			if maxFrames > 0 && frameIdx > maxFrames {
				return fmt.Errorf("audio: waveform: %w", ErrInputTooLarge)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err // already wrapped by the decoder
		}
	}
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
	read := make([]float32, decodeChunkFrames*ch)
	maxFrames := a.opts.MaxDecodedBytes / int64(bytesPerFrame(ch))

	var frameIdx int64
	for chunk := 0; ; chunk++ {
		if chunk&ctxCheckMask == 0 {
			if cerr := ctx.Err(); cerr != nil {
				return Loudness{}, fmt.Errorf("audio: loudness: %w", cerr)
			}
		}
		n, rerr := st.read(read)
		frames := n / ch
		if frames > 0 {
			meter.WriteFloat32(rs.process(read[:frames*ch], ch))
			frameIdx += int64(frames)
			if maxFrames > 0 && frameIdx > maxFrames {
				return Loudness{}, fmt.Errorf("audio: loudness: %w", ErrInputTooLarge)
			}
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				break
			}
			return Loudness{}, rerr
		}
	}
	meter.WriteFloat32(rs.flush())
	meter.Finalize()
	res := meter.Loudness()
	return Loudness{
		IntegratedLUFS:  res.IntegratedLoudness,
		TruePeakDBTP:    res.TruePeak,
		LoudnessRangeLU: res.LoudnessRange,
	}, nil
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
	return int64(info.Duration.Seconds() * float64(info.SampleRate))
}

// bytesPerFrame is the in-memory float32 cost of one inter-channel frame.
func bytesPerFrame(ch int) int {
	if ch < 1 {
		ch = 1
	}
	return ch * 4
}
