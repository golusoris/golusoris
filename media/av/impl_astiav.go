//go:build ignore
// +build ignore

// Activate: remove the go:build ignore line above, then:
//   go get github.com/asticode/go-astiav
//   go mod tidy

package av

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/asticode/go-astiav"
)

type astiavProber struct{}
type astiavTranscoder struct{ opts Options }

// NewProber returns an FFmpeg-backed Prober.
func NewProber(opts Options) (Prober, error) {
	setLogLevel(opts.LogLevel)
	return &astiavProber{}, nil
}

// NewTranscoder returns an FFmpeg-backed Transcoder.
func NewTranscoder(opts Options) (Transcoder, error) {
	setLogLevel(opts.LogLevel)
	return &astiavTranscoder{opts: opts}, nil
}

func setLogLevel(level string) {
	switch strings.ToLower(level) {
	case "quiet":
		astiav.SetLogLevel(astiav.LogLevelQuiet)
	case "error":
		astiav.SetLogLevel(astiav.LogLevelError)
	case "info":
		astiav.SetLogLevel(astiav.LogLevelInfo)
	default:
		astiav.SetLogLevel(astiav.LogLevelWarning)
	}
}

func (p *astiavProber) Probe(_ context.Context, path string) (MediaInfo, error) {
	fc := astiav.AllocFormatContext()
	if fc == nil {
		return MediaInfo{}, fmt.Errorf("av: alloc format context")
	}
	defer fc.Free()

	if err := fc.OpenInput(path, nil, nil); err != nil {
		return MediaInfo{}, fmt.Errorf("av: open %s: %w", path, err)
	}
	defer fc.CloseInput()

	if err := fc.FindStreamInfo(nil); err != nil {
		return MediaInfo{}, fmt.Errorf("av: find stream info: %w", err)
	}

	info := MediaInfo{
		Format:   fc.InputFormat().Name(),
		Duration: time.Duration(fc.Duration()) * time.Microsecond,
		BitRate:  fc.BitRate(),
	}
	for _, s := range fc.Streams() {
		cp := s.CodecParameters()
		si := StreamInfo{
			Index:     s.Index(),
			CodecName: astiav.FindDecoder(cp.CodecID()).Name(),
			BitRate:   cp.BitRate(),
			Duration:  time.Duration(s.Duration()) * time.Microsecond,
		}
		switch cp.MediaType() {
		case astiav.MediaTypeVideo:
			si.CodecType = "video"
			si.Width = cp.Width()
			si.Height = cp.Height()
			r := s.AvgFrameRate()
			if r.Den() != 0 {
				si.FrameRate = float64(r.Num()) / float64(r.Den())
			}
		case astiav.MediaTypeAudio:
			si.CodecType = "audio"
			si.Channels = cp.ChannelLayout().NbChannels()
			si.SampleRate = cp.SampleRate()
		}
		info.Streams = append(info.Streams, si)
	}
	return info, nil
}

func (t *astiavTranscoder) Transcode(_ context.Context, inPath, outPath string, opts TranscodeOptions) error {
	// Minimal passthrough via ffmpeg CLI wrapper — full astiav encode pipeline
	// is architecture-specific; implement per your codec requirements.
	_ = inPath
	_ = outPath
	_ = opts
	return fmt.Errorf("av: full transcode pipeline not yet implemented; use Probe for probing")
}
