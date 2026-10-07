// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package av defines backend-neutral audio/video probe and transcode contracts.
//
// The repository does not ship an FFmpeg runtime backend. Applications provide
// [Prober] and [Transcoder] implementations. The constructors fail explicitly
// so an unavailable prototype cannot be mistaken for a working optional build.
package av

import (
	"context"
	"errors"
	"time"
)

// ErrCGORequired is returned because no runtime AV backend is bundled.
// The name is retained for API compatibility.
var ErrCGORequired = errors.New("av: no runtime backend is bundled; inject a Prober or Transcoder")

// StreamInfo describes a single audio or video stream within a media file.
type StreamInfo struct {
	Index      int
	CodecName  string
	CodecType  string // "video" | "audio" | "subtitle"
	Width      int    // video only
	Height     int    // video only
	FrameRate  float64
	Channels   int // audio only
	SampleRate int // audio only
	BitRate    int64
	Duration   time.Duration
}

// MediaInfo describes a media container (file or stream).
type MediaInfo struct {
	Format   string
	Duration time.Duration
	BitRate  int64
	Streams  []StreamInfo
}

// Options is retained for the compatibility constructors. It has no effect
// while the repository ships no runtime backend.
type Options struct {
	// LogLevel is reserved for application backends.
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

type (
	stubProber     struct{}
	stubTranscoder struct{}
)

func (stubProber) Probe(_ context.Context, _ string) (MediaInfo, error) {
	return MediaInfo{}, ErrCGORequired
}

func (stubTranscoder) Transcode(_ context.Context, _, _ string, _ TranscodeOptions) error {
	return ErrCGORequired
}

// NewProber returns [ErrCGORequired]. Applications must provide a [Prober].
func NewProber(_ Options) (Prober, error) { return stubProber{}, ErrCGORequired }

// NewTranscoder returns [ErrCGORequired]. Applications must provide a
// [Transcoder].
func NewTranscoder(_ Options) (Transcoder, error) { return stubTranscoder{}, ErrCGORequired }
