# Agent guide — media/audio/

Pure-Go, **no-CGO** server-side audio analysis: probe (format/duration/sample
rate), decoded-PCM streaming, waveform peak buckets, and EBU R128 / BS.1770
loudness (LUFS + true-peak). The lightweight headless sibling to `media/av`
(FFmpeg/CGO) — pull it in without installing FFmpeg shared libs.

**Own `go.mod` sub-module** (`github.com/golusoris/golusoris/media/audio`) so its
decoder deps never enter the framework root graph.

## API

```go
a, err := audio.NewAnalyzer(opts, logger)
info, err := a.Probe(ctx, r)        // Info{Format, Duration, SampleRate, Channels}
peaks, err := a.Waveform(ctx, r, n) // PeakSet — n buckets for a waveform UI
loud, err := a.Loudness(ctx, r)     // Loudness{IntegratedLUFS, TruePeak}
```

`Format` ∈ mp3 / ogg / flac / wav (sniffed). Stateless after construction; safe
for concurrent use.

## Why a curated decoder fan-out (not faiface/beep)

`beep` is playback/speaker-oriented (needs an output device); this is a headless
server. Decode is a small set behind one `Decoder` interface: `hajimehoshi/go-mp3`,
`mewkiz/flac`, `jfreymuth/oggvorbis`, `go-audio/wav`; loudness via `exaring/ebur128`.
All pure Go, no CGO.

## Notes

- Decompression is bounded (Power-of-10) — oversized/garbage inputs return
  `ErrCorrupt` / `ErrUnknownFormat`, never an unbounded read.
- See ADR-0013 for the decoder-choice rationale.
