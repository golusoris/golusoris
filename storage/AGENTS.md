<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — storage/

Bucket interface + local-filesystem and S3 backends. Optional
`Copier` (server-side copy) + `PutPresigner` (presigned upload) interfaces;
type-assert before use.

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

type Copier interface {
    Copy(ctx, srcKey, dstKey) (Object, error) // ErrNotFound, ErrCopySameKey
}

type PutPresigner interface {
    PresignPut(ctx, key, ttl, PresignPutOptions) (PresignedRequest, error)
}
```

Presign contract: `ValidatePresignPut` = one validator for every backend.
TTL `MinPresignTTL` (1s) .. `MaxPresignTTL` (7d), else `ErrPresignTTL`.
`PresignPutOptions`: zero field = unconstrained. `ContentType`,
`ContentLength` (exact), `Metadata`, `Checksum{Algorithm, Value}` (raw
digest; `sha256`/`crc32c`/`md5`). Unsupported algorithm ->
`ErrUnsupportedChecksum`; unenforceable constraint (Azure length) ->
`ErrUnsupportedConstraint`. Never silently dropped. `PresignedRequest.Header` = headers client sends
verbatim; `Expires` = server-enforced deadline.

Conformance: `testutil/objstore.RunConformance(t, bucket, Capabilities{...})`
runs shared contract (round trip, empty object, stat/exists/delete, missing
key, list bounds, unsafe keys, URL, Copy, PresignPut) for every backend.

## Backends

| Backend | Notes |
| --- | --- |
| `NewLocalBucket(dir)` | Files on disk; `os.Root` confinement blocks traversal + symlink escape. Keys: canonical, <= 1024 bytes. Every new object gets a bounded sidecar bound to body size + SHA-256. `URL` returns `file://` |
| `NewS3Bucket(ctx, S3Options)` | S3/MinIO via aws-sdk-go-v2. `URL` returns presigned GET. Implements `Copier` + `PutPresigner`. MinIO: set `Endpoint` + `PathStyle`. |
| `gcs.New(ctx, gcs.Options, clock)` | Own module `storage/gcs`; `gcs.Module` replaces `storage.Module`. See `storage/gcs/AGENTS.md`. |
| `azblob.New(azblob.Options, clock)` | Own module `storage/azblob`; `azblob.Module` replaces `storage.Module`. See `storage/azblob/AGENTS.md`. |

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
storage.s3.presign_ttl = 15m     # URL() presigned-GET lifetime (default 15m, max 7d)
storage.s3.role_arn    = "arn:aws:iam::123456789012:role/app"   # optional
storage.s3.web_identity_token_file = "/var/run/secrets/tokens/s3"  # needs role_arn
storage.s3.role_session_name = "app"   # optional
storage.s3.sts_endpoint      = ""      # optional STS override
storage.s3.part_size           = 8388608   # 5 MiB..5 GiB (default 8 MiB)
storage.s3.multipart_threshold = 16777216  # 5 MiB..5 GiB (default 16 MiB)
storage.s3.concurrency         = 5         # 1..32 parallel parts per call
```

Static credentials: both fields or neither; half-configured pair = error.
Credentials: base = static keys or AWS default chain (env, IRSA, Pod
Identity, IMDS). `role_arn` layers STS AssumeRole on base;
`role_arn` + `web_identity_token_file` = AssumeRoleWithWebIdentity (no static
keys). Token file re-read on every refresh: rotated projected tokens work.
Credential cache refreshes before expiry.

`Put`: aws transfermanager (`feature/s3/manager` deprecated upstream).
Body < `multipart_threshold` -> one PutObject; else multipart, parts
buffered, `concurrency` workers, failed upload aborted. Unseekable +
unknown-length bodies OK; cap = 10000 x `part_size`. Peak buffer memory per
Put = (`concurrency`+1) x `part_size`. Checksum policy follows SDK client
config (`AWS_REQUEST_CHECKSUM_CALCULATION`).
`Copy`: HeadObject -> source <= 5 GiB: one CopyObject; larger: multipart
UploadPartCopy (1 GiB parts, `concurrency` bound, abort on failure). Every
request pins source ETag (`x-amz-copy-source-if-match`): concurrent
overwrite fails copy instead of mixing versions. Content type + metadata kept.
`PresignPut`: SigV4 query-signed PUT. Content type, exact length, metadata,
checksum (sha256/crc32c/md5) = signed headers; mismatch -> 403/400.
`Put` snapshots caller metadata before AWS SDK entry.
Fx construction gives AWS config loading a fixed 15-second deadline.

`URL` issues presigned GET (object need not exist; URL is signed,
not validated). `Delete` is idempotent (deleting missing key is not error). `Get`/`Stat` map S3 404 / `NoSuchKey` to `ErrNotFound`; `Exists` maps it to
`false`. `Stat`/`Exists` use HeadObject (no body fetched).

`List`: one bounded page, ascending byte-wise key order. `Limit: 0` uses
`storage.DefaultListLimit` (1000); valid explicit limits are 1 through
`storage.MaxListLimit` (1000). `StartAfter`: keys strictly after value,
byte-wise; value need not name object; validated like `Prefix`. Next page ->
last key of page as `StartAfter`. Short page != end; only empty page = end.
`storage.Walk` loops pages, max `storage.MaxWalkPages` (2^20); key not after
previous -> `storage.ErrListOrder`.
Local listing validates canonical prefix, starts at nearest fixed directory,
reads each visited directory whole and sorts, checks cancellation, caps one
call at 65536 directory entries. Budget hit -> objects found so far, or
`storage.ErrListWorkLimit` when none. One directory above budget (~32k objects
plus metadata sidecars) -> `storage.ErrListWorkLimit`; split key space.
S3: one `ListObjectsV2` page per request with `StartAfter` + `MaxKeys`; max 256
empty pages per call. S3 directory buckets list out of byte order ->
`StartAfter` page fails `storage.ErrListOrder`, never skips objects.
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
