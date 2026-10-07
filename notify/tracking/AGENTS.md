<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# notify/tracking

Email open/click tracking via signed URLs and 1×1 pixel.

## Surface

- `tracking.New(store, secret, logger)` → `(*Service, error)`.
- Nil and typed-nil stores reject. Secret requires 32 bytes; constructor clones it.
- Nil logger → `slog.Default()`; `Record` failures log at Warn.
- `svc.PixelURL(baseURL, messageID, recipient)` → signed pixel URL.
- `svc.ClickURL(baseURL, messageID, recipient, target)` → signed redirect URL.
- `svc.PixelHandler()` → serves 1×1 GIF, records open.
- `svc.ClickHandler()` → 302 → target, records click.
- `Store` iface: `Record(ctx, Event) error`.

## Notes

- Signatures are HMAC-SHA256 over `messageID|0|recipient|0|target` with
 service secret. Rotating secret invalidates outstanding
 tracking URLs.
- Pixel handler still serves GIF on bad signature (avoids broken
 renders in mail clients); it only skips `Record` call. Click
 handler rejects bad signatures with 403.
- Click handler rejects non-http(s) targets (open-redirect guard) and
 URLs without host.
- Client IP is read only from `RemoteAddr`. Run `httpx/middleware.TrustProxy`
 before tracking when deployed behind a trusted proxy; raw forwarding headers
 are never trusted here.
