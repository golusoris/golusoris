# Agent guide — storage/tus/

Mounts a tus 1.0 resumable-upload endpoint backed by a `storage.Bucket`. Wraps
`tus/tusd/v2/pkg/handler` with a Bucket-backed DataStore: chunks land in a
node-local scratch area during the upload and stream into the Bucket once on
`FinishUpload`. Opt-in module (config `storage.tus.enabled`).

## API

```go
type Handler struct{ /* ... */ }            // http.Handler over the whole tus sub-tree
func (h *Handler) Mount(r chi.Router)        // app-driven; app middleware still wraps it
func (h *Handler) BasePath() string
func (h *Handler) OnComplete(fn func(context.Context, CompletedUpload) error)

type CompletedUpload struct{ ID, Key string; Size int64; MetaData map[string]string }
```

The framework never grabs the router — the app mounts the handler so its own
middleware (auth, ratelimit) wraps the tus routes.

## Wiring

```go
fx.New(
    golusoris.Core,
    storage.Module,
    tus.Module,   // provides *tus.Handler
    fx.Invoke(func(r chi.Router, h *tus.Handler) { h.Mount(r) }),
)
```

Requires `storage.Bucket`, `*slog.Logger`, `clock.Clock`, `*config.Config`.
Config keys live under the `storage.tus` prefix (`enabled`, `base_path`,
`max_size`, `key_prefix`, `scratch`, `scratch_dir`, `upload_expiry`, the
`disable_*` flags, and the tusd timeouts). On `fx.Lifecycle` `OnStart` it runs a
bounded completion-drain goroutine; `OnStop` cancels it and best-effort sweeps
expired scratch.

## Notes

- **Node-local scratch (`scratch: "local"`).** A resumed `PATCH` must reach the
  same replica. Run single-replica or with sticky sessions until a distributed
  scratch lands. The memory locker is likewise node-local.
- **Keys are untrusted.** `defaultKeyFunc` sanitizes the upload id under
  `key_prefix`; every `KeyFunc` result passes a final `sanitizeKey` guard
  (rejects `..`, backslashes, NUL, absolute keys) before reaching the Bucket.
  Custom `KeyFunc`s MUST reject traversal too.
- `OnComplete` callbacks run in registration order after the Bucket `Put`; the
  first error fails the upload's finish response. Scratch is removed only after a
  successful Put, so an interrupted finish is retryable.
- Download is disabled by default (`disable_download: true`) — serve via the
  Bucket, not tusd.
- CORS for tus's custom headers is delegated to `httpx/cors`, not configured here.
