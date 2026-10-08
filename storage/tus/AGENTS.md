<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — storage/tus/

Mounts tus 1.0 resumable-upload endpoint backed by `storage.Bucket`. Wraps
`tus/tusd/v2/pkg/handler` with Bucket-backed DataStore. Chunks land in
node-local scratch during upload. Stream into Bucket once on
`FinishUpload`. Opt-in module (config `storage.tus.enabled`).

## API

```go
type Handler struct{ /* ... */ }
func (h *Handler) Mount(r chi.Router)
func (h *Handler) BasePath() string
func (h *Handler) OnComplete(
    id string,
    fn func(context.Context, CompletedUpload) error,
) error

type CompletedUpload struct {
    ID, Key string
    Size int64
    MetaData map[string]string
}
```

framework never grabs router — app mounts handler so its own
middleware (auth, ratelimit) wraps tus routes.

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
Config keys live under `storage.tus` prefix (`enabled`, `base_path`,
`max_size`, `key_prefix`, `scratch`, `scratch_dir`, `upload_expiry`,
`expiry_sweep_interval`, `disable_*` flags, tusd timeouts).
`max_size` defaults to a finite 5 GiB and must be positive.
`scratch` accepts only `local`; empty or unknown values fail construction.
Empty `scratch_dir`: private process temp root per Handler; removed on shutdown.
Persistent restart recovery: configure app-unique `scratch_dir`; never share
between applications.

`enabled: false`: inert handler. `Mount`: no routes. direct `ServeHTTP`: 404.

`OnStart`: one bounded worker. Drains tusd notifications. Retries durable
completion receipts. Sweeps expired scratch each `expiry_sweep_interval`.
`OnStop`: cancel, join, final best-effort expiry sweep. Join timeout returns
context error and leaves scratch open; active callback retains shared state.

## Notes

- **Node-local scratch (`scratch: "local"`).** resumed `PATCH` must reach same
  replica. Run single-replica or sticky sessions until distributed scratch.
  upload locker also node-local. Empty `scratch_dir` isolates co-located apps
  but cannot resume after process restart. Explicit app-unique path persists.
- Scratch creation uses exclusive data/info files; duplicate IDs fail without
 truncation. Info-create failure removes newly reserved data. Chunk writes
 require exact offset and poll request cancellation during source reads.
- Scratch state and raw-data opens use `os.Root`; symlink/non-regular nodes
 rejected; opened identity rechecked. Raw Get, append, completion read, abort
 share boundary. State reads context-aware and bounded: `.info` 1 MiB,
 `.complete` 2 MiB. State writes reject matching overflow.
- Scratch reads, receipt publication, and destructive cleanup check context
  before filesystem access and between multi-file phases. Pre-canceled cleanup
  preserves upload plus recovery receipt.
- **Keys are untrusted.** `defaultKeyFunc` sanitizes upload id under
 `key_prefix`; every `KeyFunc` result passes final `sanitizeKey` guard
 (rejects `..`, backslashes, NUL, absolute keys) before reaching Bucket.
 Custom `KeyFunc`s MUST reject traversal too.
- `FinishUpload`: durable `prepared` -> `putting` receipt -> destination
  reconciliation or Bucket `Put` -> `persisted` receipt -> callback chain ->
  scratch cleanup -> receipt acknowledgement.
- Recovery probes Bucket bytes, content type, and metadata against scratch.
  Exact object -> skip repeated `Put`. Missing or different object ->
  stable-key `Put` retry. No
  atomic commit across Bucket and scratch filesystem; Bucket read-after-write
  visibility bounds duplicate-call avoidance.
- `OnComplete`: ordered, durable, at-least-once. Initial delivery remains inline;
 failure fails finish response and retains receipt. Runtime worker retries from
 first failed callback. Successful stable IDs durably checkpointed; deploy-time
 insertion or reordering cannot replay completed IDs. Registration rejects
 empty/duplicate IDs, nil functions, more than 64 callbacks.
 Callback MUST idempotently key side effect by
 `CompletedUpload.ID` because crash can occur after side effect, before
 checkpoint.
- Completion metadata clones at receipt ingress and per callback. Callback map
  mutation cannot alter durable state or later callback payloads.
- Migration: replace `OnComplete(fn)` with `OnComplete("stable.id.v1", fn)` and
 handle registration error. Versioned receipts reject pre-version state; drain
 old completion receipts before upgrade.
- Completion receipt survives scratch cleanup and process restart. Expiry sweep
  never removes upload carrying completion receipt.
- Receipt acknowledgement removes receipt then fsyncs parent directory. Sync
 failure = indeterminate acknowledgement: receipt absent in current process;
 caller receives durability error.
- Directory durability: Unix `fsync`; Windows attempts `FlushFileBuffers`, then
 accepts only unsupported read-only-directory errors after file sync.
- Maintenance walks at most 256 directory entries per call and carries an
  advancing cursor across ticks; shutdown closes retained directory handles.
- Expiry activity = newest data/info mtime. Sweeper uses same per-upload lock as
  tusd, skips active request, rechecks expiry while locked. `upload_expiry` and
  `expiry_sweep_interval` must be positive.
- Download disabled by default (`disable_download: true`). Serve via Bucket,
  not tusd.
- CORS for tus's custom headers is delegated to `httpx/cors`, not configured here.
