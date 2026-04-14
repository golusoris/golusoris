# notify/apns2

Apple Push Notification service (HTTP/2 + token auth) sender for
`notify.Sender`.

## Surface

- `apns2.NewSender(Options)` → `*Sender`.
- `Options{KeyID, TeamID, Topic, P8Key, Production, DefaultPushType,
  DefaultPriority, Expiration, HTTPClient}`.
- `PushType` / `Priority` enums.
- `apns2.ErrUnregistered` — sentinel for 410 / `Unregistered`; apps
  use `errors.Is(err, apns2.ErrUnregistered)` to clean up dead devices.

## Notes

- **Auth**: token-based only (no .pem cert flow). `P8Key` is the
  PEM-encoded PKCS8 ECDSA P-256 private key Apple gives you in
  `AuthKey_<KeyID>.p8`. JWT is ES256-signed with `{iss: TeamID, iat}`
  claims + `{kid: KeyID}` header.
- **JWT cache**: regenerated every 50 min (APNs accepts tokens up to
  60 min per spec). Concurrent Send calls share one JWT via a mutex.
- Dep: `golang-jwt/jwt/v5` (already in go.mod); `golang.org/x/net/http2`
  for explicit h2 configuration on the default client.
- **Routing**: each `msg.To[i]` is a device token (hex). The spec is
  single-device per request — this sender loops and short-circuits on
  the first non-success.
- **Payload**: `{aps:{alert:{title,body}, ...}, <custom keys>...}`.
  Metadata keys are split:
  - `apns-push-type` / `apns-priority` / `apns-id` / `apns-collapse-id`
    → request headers
  - `apns-sound` / `apns-badge` / `apns-thread-id` /
    `apns-content-available` → aps fields
  - everything else → top-level custom payload keys
- **Production vs sandbox**: `Options.Production=true` targets
  `api.push.apple.com`; false targets `api.sandbox.push.apple.com`.
  Match the cert you used to build your app — sandbox tokens fail
  against production and vice versa with HTTP 400 `BadDeviceToken`.
- **Expiration**: `Options.Expiration > 0` sets `Apns-Expiration` to
  `now + Expiration`; 0 means "attempt once, drop on failure".
- **HTTP/2**: the default client is configured via
  `http2.ConfigureTransport` so h2 is explicit rather than relying on
  TLS ALPN negotiation; apps that supply a custom `HTTPClient` are
  responsible for h2 themselves.
