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
| S3 (planned) | `storage/s3` sub-package using aws-sdk-go-v2 |
| GCS (planned) | `storage/gcs` sub-package |

## Don't

- Don't call `URL` on a `LocalBucket` expecting an HTTP URL — serve with `httpx/rangeserve` instead.
- Don't store raw user-supplied filenames as keys — sanitize first (no `../`, no null bytes).
- Don't use `LocalBucket` in multi-replica deployments — use a shared S3/GCS bucket.
