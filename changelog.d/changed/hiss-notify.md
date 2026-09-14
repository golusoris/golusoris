- **BREAKING**: `notify/tracking.New` takes a `*slog.Logger` so `Store.Record` failures in the pixel/click handlers are logged instead of discarded (HISS-07 burn-down, group notify). A nil logger falls back to `slog.Default()`.

  ```go
  // before
  svc := tracking.New(store, secret)
  // after
  svc := tracking.New(store, secret, logger) // or nil → slog.Default()
  ```

- `notify/*` senders, `notify/bounce`, `notify/inbound`, `webhooks/out` and `realtime/webrtc` now surface HTTP body / peer-connection close errors via `core/errors.CloseInto` / `CloseJoin` (a close failure is returned when nothing else failed); `webhooks/out` no longer panics when `crypto/rand` fails — `Dispatch` returns the error.
