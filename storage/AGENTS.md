# Agent guide — storage/

Bucket interface + local-filesystem backend. Cloud backends (S3, GCS, Azure
Blob) implement the same `Bucket` interface.

## Bucket interface

```go
type Bucket interface {
    Put(ctx, key, r, PutOptions) (Object, error)
    Get(ctx, key) (io.ReadCloser, Object, error)
    Delete(ctx, key) error
    Exists(ctx, key) (bool, error)
    List(ctx, ListOptions) ([]Object, error)
    URL(ctx, key) (string, error)
}
```

## Backends

| Backend | Notes |
|---|---|
| `NewLocalBucket(dir)` | Files on disk; path-traversal protected. `URL` returns `file://` |
| `NewS3Bucket(ctx, S3Options)` | S3/MinIO via aws-sdk-go-v2. `URL` returns a presigned GET. MinIO: set `Endpoint` + `PathStyle`. |
| GCS (planned) | `storage/gcs` sub-package |

## S3 backend

`S3Bucket` lives in the root `storage` package (not a sub-package) so
`Module` can wire it without an import cycle. Select it via config:

```
storage.backend = "s3"
storage.s3.bucket      = "uploads"
storage.s3.region      = "us-east-1"
storage.s3.endpoint    = "http://localhost:9000"  # MinIO; omit for real S3
storage.s3.access_key  = "..."   # empty = AWS default credential chain
storage.s3.secret_key  = "..."
storage.s3.path_style  = true    # required for MinIO
storage.s3.presign_ttl = 15m     # URL() presigned-GET lifetime (default 15m)
```

`URL` issues a presigned GET (the object need not exist; the URL is signed,
not validated). `Delete` is idempotent (deleting a missing key is not an
error). `Get`/`Exists` map S3 404 / `NoSuchKey` to `ErrNotFound` / `false`.

## Don't

- Don't call `URL` on a `LocalBucket` expecting an HTTP URL — serve with `httpx/rangeserve` instead.
- Don't store raw user-supplied filenames as keys — sanitize first (no `../`, no null bytes).
- Don't use `LocalBucket` in multi-replica deployments — use a shared S3/GCS bucket.
