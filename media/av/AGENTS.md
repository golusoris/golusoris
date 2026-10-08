<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# media/av

Audio/video SPI. No runtime backend shipped. No fx wiring.

## API

- `Prober`: path -> bounded `MediaInfo`.
- `Transcoder`: path plus `TranscodeOptions` -> output path.
- Constructors: always `ErrCGORequired`; compatibility surface only.
- Application: implement and inject `Prober` or `Transcoder`.
- Backend: honor context; bound processing time, streams, output, and workers.
- Paths: validate; stage untrusted uploads in isolated temporary storage.
