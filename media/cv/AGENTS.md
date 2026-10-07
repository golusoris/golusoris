<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# media/cv

Computer-vision SPI. No runtime backend shipped. No fx wiring.

## API

- `Analyzer`: DetectFaces, DetectObjects, Thumbnail, Close.
- `NewAnalyzer`: always `ErrCGORequired`; compatibility constructor only.
- Application: implement and inject `Analyzer`.
- Models: application-owned, version-pinned, integrity-verified.
- Backend: honor context; bound input bytes, decoded pixels, model size, output,
  and workers.
