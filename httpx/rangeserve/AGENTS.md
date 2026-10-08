<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — httpx/rangeserve/

HTTP range serving for large files. `http.ServeContent`: single and multipart
ranges; Last-Modified from `modTime`; ETag conditionals only when caller sets
the response ETag before dispatch.

## API

```go
// Serve from any Opener (e.g. LocalBucket):
mux.Handle("/videos/{id}", rangeserve.HandlerWithLogger(opener, func(r *http.Request) string {
    return chi.URLParam(r, "id")
}, logger))

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

Implement this on your storage backend or use thin adapter around
`storage.LocalBucket`.

## Errors

- `os.ErrNotExist`: 404.
- Other open error: log internal cause; send sanitized RFC 9457 500.
- Close error: warning log after response.
- `Handler`: uses `slog.Default`.
- `HandlerWithLogger`: explicit application logger.

## Don't

- Don't use `rangeserve` for small JSON responses — it adds overhead.
- Don't serve files without authentication from sensitive paths.
