# Agent guide — media/av/

Audio/video probing + transcoding backed by FFmpeg via go-astiav (CGO).
Direct-import constructors — **no fx wiring**; apps build a `Prober` / `Transcoder`
and share them.

## API

```go
p, err := av.NewProber(av.Options{})            // LogLevel default "warning"
info, err := p.Probe(ctx, "video.mp4")          // MediaInfo: Format, Duration, Streams[]

t, err := av.NewTranscoder(av.Options{})
err = t.Transcode(ctx, "in.mp4", "out.webm", av.TranscodeOptions{
    VideoCodec: "vp9", AudioCodec: "opus",      // empty codec = stream copy
})
```

`MediaInfo.Streams` holds `StreamInfo` (codec, dims, frame rate, channels,
sample rate, bit rate, duration) per audio/video/subtitle stream.

## Why go-astiav

- Thin, low-level binding straight onto the FFmpeg C API (libav*), giving full
  codec/format reach without shelling out to an `ffmpeg` binary or its parsing fragility.

## Notes

- **CGO-gated, own go.mod sub-module.** Shipped constructors are stubs returning
  `ErrCGORequired`; activate per the package doc (drop `//go:build ignore` from
  `impl_astiav.go`, `go get github.com/asticode/go-astiav`, `go mod tidy`).
- Requires FFmpeg shared libs: `libavcodec-dev` + friends (apt) / `ffmpeg` (brew).
- `Probe`/`Transcode` take file **paths**, not byte slices — stage uploads to disk
  (or a temp file) first.
