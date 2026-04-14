// Package av wraps FFmpeg via go-astiav (CGO) for audio/video transcoding and
// media probing. FFmpeg shared libraries must be installed before use.
//
// Install system deps (Debian/Ubuntu):
//
//	apt-get install libavcodec-dev libavformat-dev libavutil-dev \
//	    libswscale-dev libswresample-dev
//
// Install system deps (macOS):
//
//	brew install ffmpeg
//
// Activate implementation:
//
//	1. Remove //go:build ignore from media/av/impl_astiav.go
//	2. Add the dep: go get github.com/asticode/go-astiav
//	3. go mod tidy
//
// Usage:
//
//	p, err := av.NewProber(av.Options{})
//	info, err := p.Probe(ctx, "video.mp4")
//
//	t, err := av.NewTranscoder(av.Options{})
//	err = t.Transcode(ctx, "in.mp4", "out.webm", av.TranscodeOptions{
//	    VideoCodec: "vp9",
//	    AudioCodec: "opus",
//	})
package av

import (
	"context"
	"errors"
	"time"
)

// ErrCGORequired is returned when the go-astiav implementation is not activated.
var ErrCGORequired = errors.New("av: CGO implementation not activated; see package doc")

// StreamInfo describes a single audio or video stream within a media file.
type StreamInfo struct {
	Index     int
	CodecName string
	CodecType string // "video" | "audio" | "subtitle"
	Width     int    // video only
	Height    int    // video only
	FrameRate float64
	Channels  int           // audio only
	SampleRate int          // audio only
	BitRate   int64
	Duration  time.Duration
}

// MediaInfo describes a media container (file or stream).
type MediaInfo struct {
	Format   string
	Duration time.Duration
	BitRate  int64
	Streams  []StreamInfo
}

// Options configures av helpers.
type Options struct {
	// LogLevel controls FFmpeg log verbosity ("quiet", "error", "warning", "info").
	// Default: "warning".
	LogLevel string
}

// TranscodeOptions fine-tunes a transcode operation.
type TranscodeOptions struct {
	// VideoCodec is the output video codec (e.g. "h264", "vp9", "av1"). Empty = copy.
	VideoCodec string
	// AudioCodec is the output audio codec (e.g. "aac", "opus", "mp3"). Empty = copy.
	AudioCodec string
	// VideoBitRate in bits/s (0 = codec default).
	VideoBitRate int64
	// AudioBitRate in bits/s (0 = codec default).
	AudioBitRate int64
	// Width / Height for video scaling (0 = source dimensions).
	Width, Height int
	// ExtraArgs are passed verbatim to the FFmpeg encoder.
	ExtraArgs []string
}

// Prober probes media file metadata without decoding the full stream.
type Prober interface {
	Probe(ctx context.Context, path string) (MediaInfo, error)
}

// Transcoder transcodes media files from one format/codec to another.
type Transcoder interface {
	Transcode(ctx context.Context, inPath, outPath string, opts TranscodeOptions) error
}

type stubProber struct{}
type stubTranscoder struct{}

func (stubProber) Probe(_ context.Context, _ string) (MediaInfo, error) {
	return MediaInfo{}, ErrCGORequired
}
func (stubTranscoder) Transcode(_ context.Context, _, _ string, _ TranscodeOptions) error {
	return ErrCGORequired
}

// NewProber returns a [Prober] backed by FFmpeg.
// When the CGO implementation is not activated it returns an error.
func NewProber(_ Options) (Prober, error) { return stubProber{}, ErrCGORequired }

// NewTranscoder returns a [Transcoder] backed by FFmpeg.
// When the CGO implementation is not activated it returns an error.
func NewTranscoder(_ Options) (Transcoder, error) { return stubTranscoder{}, ErrCGORequired }
