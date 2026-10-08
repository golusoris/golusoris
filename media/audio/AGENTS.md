<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — media/audio/

Pure-Go, **no-CGO** server-side audio analysis: probe (format/duration/sample
rate), decoded-PCM streaming, waveform peak buckets, and EBU R128 / BS.1770
loudness (LUFS + true-peak). lightweight headless sibling to `media/av`
(FFmpeg/CGO) — pull it in without installing FFmpeg shared libs.

**Own `go.mod` sub-module** (`github.com/golusoris/golusoris/media/audio`) so its
decoder deps never enter framework root graph.

## API

```go
a, err := audio.NewAnalyzer(opts, logger)
info, err := a.Probe(ctx, r, hint)        // Info{Format, Duration, SampleRate, Channels}
peaks, err := a.Waveform(ctx, r, hint, n) // PeakSet — n buckets for a waveform UI
loud, err := a.Loudness(ctx, r, hint)     // Loudness{IntegratedLUFS, TruePeak}
```

`Format` ∈ mp3 / ogg / flac / wav (sniffed). Stateless after construction; safe
for concurrent use.

## Why a curated decoder fan-out (not faiface/beep)

`beep` is playback/speaker-oriented (needs output device); this is headless
server. Decode is small set behind one `Decoder` interface: `hajimehoshi/go-mp3`,
`mewkiz/flac`, `jfreymuth/oggvorbis`, `go-audio/wav`; loudness via `exaring/ebur128`.
All pure Go, no CGO.

## Notes

- Decompression is bounded (Power-of-10) — oversized/garbage inputs return
 `ErrCorrupt` / `ErrUnknownFormat`, never unbounded read.
- `max_input_bytes` defaults 512 MiB. one context-aware encoded-input boundary
 wraps every codec before sniff and decoder construction. exact limit succeeds;
 first byte beyond limit returns `ErrInputTooLarge`.
- `max_channels` defaults 64. forged WAV/AIFF channel counts reject before
 scratch allocation. scratch size also fits `max_decoded_bytes`.
- `max_decoded_bytes` exact boundary accepted; first sample beyond rejects
 before waveform/loudness consumer work.
- `max_peak_buckets` defaults to 1,048,576. Requests above it reject before
 input parsing or waveform allocation.
- `backend` empty -> `pureGo`; every other value except exact `pureGo` rejects.
- WAV/AIFF Probe retains seekable input; metadata read does not materialize PCM.
- go-audio emits eight-bit WAV/AIFF PCM unsigned. normalization subtracts 128.
- Unknown-duration waveforms retain at most two temporal summaries per output
 bucket. adjacent summaries compact as input grows; EOF rebin prevents tail
 collapse while memory stays O(buckets).
- Low-rate resampling uses bounded input chunks and output-frame budget. forged
 sample rates cannot trigger giant allocations.
- See ADR-0013 for decoder-choice rationale.
