// Package audio is golusoris's pure-Go, no-CGO server-side audio analysis layer:
// probe (format/duration/rate), decoded-PCM streaming, waveform peak buckets and
// EBU R128 / BS.1770 loudness (integrated LUFS + true-peak + loudness range).
//
// It is the lightweight headless sibling of media/av: av owns heavy
// FFmpeg/CGO transcode, audio owns the dependency-free analysis path an app
// can pull in without installing FFmpeg shared libraries. All decoders are
// pure Go (mp3/ogg/flac/wav/aiff), so it ships a working implementation rather
// than a CGO-gated stub.
//
// All methods stream from an io.Reader and never touch the speaker or the
// filesystem implicitly. Untrusted input is bounded by [Options] before any
// PCM is materialized.
//
//	a, err := audio.NewAnalyzer(audio.Options{}, logger)
//	info, err := a.Probe(ctx, r, audio.FormatMP3)       // cheap header read
//	peaks, err := a.Waveform(ctx, r, "", 2048)          // waveform buckets
//	loud, err := a.Loudness(ctx, r, audio.FormatWAV)    // EBU R128 LUFS
//
// An [Analyzer] is immutable after construction and safe for concurrent use.
package audio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"
)

// Format identifies a decodable container/codec.
type Format string

// Supported formats.
const (
	FormatMP3  Format = "mp3"
	FormatOGG  Format = "ogg"
	FormatFLAC Format = "flac"
	FormatWAV  Format = "wav"
	FormatAIFF Format = "aiff"
)

// Sentinel errors. Callers should match with errors.Is.
var (
	// ErrUnknownFormat means no decoder recognized the input header. Wrapped
	// errors carry the "audio:" package prefix; the sentinels do not.
	ErrUnknownFormat = errors.New("unknown or unsupported format")
	// ErrInputTooLarge means decoded PCM would exceed Options.MaxDecodedBytes.
	ErrInputTooLarge = errors.New("decoded size exceeds limit")
	// ErrTooLong means the input duration exceeds Options.MaxDuration.
	ErrTooLong = errors.New("duration exceeds limit")
	// ErrCorrupt means a decoder rejected the bytes as malformed.
	ErrCorrupt = errors.New("corrupt or truncated input")
)

// Info is header-level metadata produced by Probe.
type Info struct {
	Format     Format
	Duration   time.Duration
	SampleRate int
	Channels   int
	BitDepth   int   // 0 when compressed/unknown
	BitRate    int64 // bits/s, best-effort (0 when unknown)
}

// PeakSet is downsampled min/max amplitude per bucket for waveform rendering.
// Min and Max are mono-mixed and clamped to the range [-1, 1].
type PeakSet struct {
	SampleRate int
	Channels   int
	Buckets    int
	Min        []float32 // len == Buckets
	Max        []float32 // len == Buckets
}

// Loudness is an EBU R128 / BS.1770-4 measurement.
type Loudness struct {
	IntegratedLUFS  float64 // gated integrated loudness
	TruePeakDBTP    float64 // max true peak across channels, dBTP
	LoudnessRangeLU float64 // LRA in LU
}

// Analyzer is the headless audio facade.
type Analyzer interface {
	// Probe reads only header/metadata — duration, format, rate, channels.
	Probe(ctx context.Context, r io.Reader, hint Format) (Info, error)
	// Waveform decodes and returns buckets min/max peak pairs. A buckets value
	// of 0 uses Options.DefaultPeakBuckets.
	Waveform(ctx context.Context, r io.Reader, hint Format, buckets int) (PeakSet, error)
	// Loudness measures EBU R128 integrated LUFS + true peak (resamples to 48k).
	Loudness(ctx context.Context, r io.Reader, hint Format) (Loudness, error)
}

// analyzer is the concrete pure-Go Analyzer.
type analyzer struct {
	opts   Options
	logger *slog.Logger
}

// NewAnalyzer builds a pure-Go Analyzer. logger must be non-nil.
func NewAnalyzer(opts Options, logger *slog.Logger) (Analyzer, error) {
	if logger == nil {
		return nil, errors.New("audio: nil logger")
	}
	opts = opts.withDefaults() // non-positive caps fall back to package defaults
	logger.Debug(
		"audio: analyzer ready",
		slog.Int64("max_decoded_bytes", opts.MaxDecodedBytes),
		slog.Int("default_peak_buckets", opts.DefaultPeakBuckets),
	)
	return &analyzer{opts: opts, logger: logger}, nil
}

// Probe opens the stream header-only and reports metadata.
func (a *analyzer) Probe(ctx context.Context, r io.Reader, hint Format) (Info, error) {
	if err := ctx.Err(); err != nil {
		return Info{}, fmt.Errorf("audio: probe: %w", err)
	}
	st, err := a.open(r, hint)
	if err != nil {
		return Info{}, err
	}
	info := st.info()
	if err = a.checkDuration(info.Duration); err != nil {
		return Info{}, err
	}
	return info, nil
}

// Waveform decodes the stream into buckets min/max peaks.
func (a *analyzer) Waveform(ctx context.Context, r io.Reader, hint Format, buckets int) (PeakSet, error) {
	if buckets <= 0 {
		buckets = a.opts.DefaultPeakBuckets
	}
	st, err := a.open(r, hint)
	if err != nil {
		return PeakSet{}, err
	}
	if err = a.checkDuration(st.info().Duration); err != nil {
		return PeakSet{}, err
	}
	return a.waveform(ctx, st, buckets)
}

// Loudness decodes, resamples to 48k stereo, and runs EBU R128.
func (a *analyzer) Loudness(ctx context.Context, r io.Reader, hint Format) (Loudness, error) {
	st, err := a.open(r, hint)
	if err != nil {
		return Loudness{}, err
	}
	if err = a.checkDuration(st.info().Duration); err != nil {
		return Loudness{}, err
	}
	return a.loudness(ctx, st)
}

// checkDuration enforces Options.MaxDuration when set and the duration is known.
func (a *analyzer) checkDuration(d time.Duration) error {
	if a.opts.MaxDuration > 0 && d > 0 && d > a.opts.MaxDuration {
		return fmt.Errorf("audio: %w: %s > %s", ErrTooLong, d, a.opts.MaxDuration)
	}
	return nil
}
