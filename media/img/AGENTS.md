<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# media/img

Image-processing SPI. No runtime backend shipped. No fx wiring. `pipeline/`
accepts injected `Processor`.

## API

- `Processor`: Resize, Convert, Optimize, Info, Close.
- Formats: JPEG, PNG, WEBP, AVIF, GIF, TIFF.
- `NewProcessor`: always `ErrCGORequired`; compatibility constructor only.
- Application: implement `Processor`; inject into pipeline.
- Backend: own lifecycle, concurrency bound, input byte bound, decoded-pixel
  bound, cancellation contract.
- Decoder: untrusted-data boundary.
- Govips dependency: integration-test fixture only. Not runtime implementation.
