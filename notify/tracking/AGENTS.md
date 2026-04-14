# notify/tracking

Email open/click tracking via signed URLs and a 1×1 pixel.

## Surface

- `tracking.New(store, secret)` → `*Service`.
- `svc.PixelURL(baseURL, messageID, recipient)` → signed pixel URL.
- `svc.ClickURL(baseURL, messageID, recipient, target)` → signed redirect URL.
- `svc.PixelHandler()` → serves 1×1 GIF, records open.
- `svc.ClickHandler()` → 302 → target, records click.
- `Store` iface: `Record(ctx, Event) error`.

## Notes

- Signatures are HMAC-SHA256 over `messageID|0|recipient|0|target` with
  the service secret. Rotating the secret invalidates outstanding
  tracking URLs.
- Pixel handler still serves the GIF on bad signature (avoids broken
  renders in mail clients); it only skips the `Record` call. Click
  handler rejects bad signatures with 403.
- Click handler rejects non-http(s) targets (open-redirect guard) and
  URLs without a host.
- Client IP is read from `X-Forwarded-For` if present (first value),
  otherwise `RemoteAddr`. Deploy behind a trusted proxy to avoid
  spoofing.
