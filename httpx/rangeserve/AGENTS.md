# Agent guide — httpx/rangeserve/

HTTP range-request serving for large files (video playback, resumable
downloads). Delegates to `http.ServeContent` (RFC 7233: single + multi-part
ranges, ETag, Last-Modified, If-Range, If-None-Match).

## API

```go
// Serve from any Opener (e.g. LocalBucket):
mux.Handle("/videos/{id}", rangeserve.Handler(opener, func(r *http.Request) string {
    return chi.URLParam(r, "id")
}))

// Serve a single file from disk:
rangeserve.ServeFile(w, r, "/var/media/movie.mp4")

// Serve from an in-memory io.ReadSeeker:
rangeserve.ServeReader(w, r, "movie.mp4", modTime, bytes.NewReader(data))
```

## Opener interface

```go
type Opener interface {
    Open(ctx, key) (io.ReadSeekCloser, time.Time, error)
}
```

Implement this on your storage backend or use a thin adapter around
`storage.LocalBucket`.

## Don't

- Don't use `rangeserve` for small JSON responses — it adds overhead.
- Don't serve files without authentication from sensitive paths.
