<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — storage/

Bucket interface + local-filesystem and S3 backends. Planned cloud backends
use same `Bucket` interface.

## Bucket interface

```go
type Bucket interface {
    Put(ctx, key, r, PutOptions) (Object, error)
    Get(ctx, key) (io.ReadCloser, Object, error)
    Delete(ctx, key) error
    Exists(ctx, key) (bool, error)
    Stat(ctx, key) (Object, error)
    List(ctx, ListOptions) ([]Object, error)
    URL(ctx, key) (string, error)
}
```

## Backends

| Backend | Notes |
| --- | --- |
| `NewLocalBucket(dir)` | Files on disk; `os.Root` confinement blocks traversal + symlink escape. Keys: canonical, <= 1024 bytes. Every new object gets a bounded sidecar bound to body size + SHA-256. `URL` returns `file://` |
| `NewS3Bucket(ctx, S3Options)` | S3/MinIO via aws-sdk-go-v2. `URL` returns presigned GET. MinIO: set `Endpoint` + `PathStyle`. |
| GCS (planned) | `storage/gcs` sub-package |

## S3 backend

`S3Bucket` lives in root `storage` package (not sub-package) so
`Module` can wire it without import cycle. Select it via config:

```ini
storage.backend = "s3"
storage.s3.bucket      = "uploads"
storage.s3.region      = "us-east-1"
storage.s3.endpoint    = "http://localhost:9000"  # MinIO; omit for real S3
storage.s3.access_key  = "..."   # empty = AWS default credential chain
storage.s3.secret_key  = "..."
storage.s3.path_style  = true    # required for MinIO
storage.s3.presign_ttl = 15m     # URL() presigned-GET lifetime (default 15m)
```

Static credentials: both fields or neither; half-configured pair = error.
`Put` snapshots caller metadata before AWS SDK entry.
Fx construction gives AWS config loading a fixed 15-second deadline.

`URL` issues presigned GET (object need not exist; URL is signed,
not validated). `Delete` is idempotent (deleting missing key is not error). `Get`/`Stat` map S3 404 / `NoSuchKey` to `ErrNotFound`; `Exists` maps it to
`false`. `Stat`/`Exists` use HeadObject (no body fetched).

`List`: bounded snapshot, not exhaustive enumeration. `Limit: 0` uses
`storage.DefaultListLimit` (1000); valid explicit limits are 1 through
`storage.MaxListLimit` (1000). Local listing validates a canonical prefix,
starts at its nearest fixed directory, checks cancellation, and caps visited
directory entries. Sparse scans that exhaust the work budget return
`storage.ErrListWorkLimit`; narrow the prefix and retry.
S3 sends one `ListObjectsV2` request with `MaxKeys`; no automatic
continuation token inside one call. Narrow `Prefix` or partition application
keys when more than one bounded snapshot is needed.
Both backends snapshot caller metadata. `Get` and `Stat` return persisted
content type and metadata. `List` returns identity, size, ETag, and timestamp
only because S3's listing API omits object metadata; call `Stat` when needed.
S3 `Put` counts consumed bytes while preserving seek support; returned `Size`
feeds durable TUS completion receipts.

Local `Put`: per-base lock -> bounded orphan-stage recovery -> staged body +
sidecar sync -> durable transaction journal -> metadata-first publication ->
parent sync -> journal cleanup. Lock opens through same `os.Root` as mutation.
Stages live in reserved `.golusoris-staging`; at most 16 entries accepted.
Publication uses destination-absent renames; no POSIX replacement-atomicity
assumption on Windows. Recovery: exact body/sidecar digest match -> commit;
otherwise restore deterministic backups. `Get` / `Stat`: context-aware body
hash; mismatch -> fail closed. Object, metadata, lock, stage nodes: pre-open
type and opened-identity checks.
Directory durability: Unix `fsync`; Windows attempts `FlushFileBuffers`, then
accepts only unsupported read-only-directory errors after file sync.
Parent-sync failure = indeterminate commit: replacement visible; `Put` returns
durability error for caller reconciliation. Internal `.golusoris-put-*.tmp`
and `.golusoris-meta-<sha256>.json`, `.golusoris-lock`,
`.golusoris-staging`, and transaction journal names are reserved and omitted
from listings. `Delete`: object + sidecar removal before shared-parent sync.
Local operations check context before filesystem access and before mutations.
`Delete`: remove -> parent-directory fsync. Sync failure = indeterminate commit:
object absent; caller receives durability error for reconciliation.
`LocalBucket` values remain comparable; private durability hooks are pointer-backed.

## Don't

- Don't call `URL` on `LocalBucket` expecting HTTP URL — serve with `httpx/rangeserve` instead.
- Don't store raw user-supplied filenames as keys — sanitize first (no `../`, no null bytes).
- Don't use `ListOptions{Limit: 0}` as exhaustive scan; zero now means
 finite default, and oversized or negative limits fail validation.
- Don't use `LocalBucket` in multi-replica deployments — use shared S3/GCS bucket.
- All backends validate keys through `storage.CleanKey`; `storage/safety`
  re-exports same contract. Empty prefixes are valid only for `List`.
