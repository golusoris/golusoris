# ADR-0013: media/audio dependency choice

- **Status**: Accepted
- **Date**: 2026-06-19
- **Deciders**: @lusoris
- **Tags**: media, audio

## Context

The `media/audio` module needs a dependency. Per principles.md §2 the choice must favour a maintained, server-appropriate library with a lean footprint, justified against ecosystem alternatives.

## Decision

Use **`github.com/hajimehoshi/go-mp3 (+ github.com/jfreymuth/oggvorbis + github.com/mewkiz/flac + github.com/go-audio/wav for decode; github.com/exaring/ebur128 for loudness) — a small curated fan-out behind one Decoder interface, NOT a single mega-dep. Primary/headline dep is go-mp3.`** (go-mp3 v0.3.4 (2022-11-02); oggvorbis latest tag ~v1.0.5; mewkiz/flac v1.0.13 (2025-07-11); go-audio/wav v1.1.0 (2022-05-22); exaring/ebur128 UNTAGGED — pin to a commit SHA from main (repo created 2026-02-17, MIT). All resolved together via go 1.26 modules in the sub-module's own go.mod.). A SERVER framework needs decode + measure, never playback. The catalog hint (beep) is built around speaker output (oto) — useless headless — and its decoding is itself a wrapper over the very libraries we pick directly, so taking beep would add an unused playback/DSP graph and an oto transitive dep for zero analysis benefit. The chosen fan-out is the de-facto pure-Go audio stack: go-mp3 (hajimehoshi, the same decoder beep/oto use), jfreymuth/oggvorbis, mewkiz/flac (actively maintained, v1.0.13 in 2025-07), go-audio/wav (mature, MIT). Each is small, single-purpose, MIT/BSD, zero-CGO, and composes behind one internal Decoder interface so format support is additive. For loudness, exaring/ebur128 is the only pure-Go EBU R128/BS.1770-4 implementation (integrated LUFS + true-peak + LRA, allocation-free steady state) and keeps the no-CGO promise. Net: no system libraries, no CGO, no FFmpeg install, and the module ships a working impl rather than a CGO-gated stub — which is what makes it worth having alongside media/av.

## Alternatives considered

| Option | Pros | Cons | Why not chosen |
|---|---|---|---|


## Consequences

See `media/audio/AGENTS.md` for the resulting API + config surface. The dependency is pinned and tracked by Renovate; revisit if it goes unmaintained or a better-fit library appears.
