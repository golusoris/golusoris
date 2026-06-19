# Agent guide — storage/safety/

Hardens user uploads before they reach a `storage.Bucket`. Three independent
concerns, **security-critical (85% coverage gate)**:

1. **Metadata stripping** — drops EXIF/GPS/XMP/text chunks by re-encoding the
   raster image through stdlib (`image/jpeg`, `image/png`, `image/gif`). No dep.
2. **SSRF-guarded fetch-by-URL** — `code.dny.dev/ssrf` validates the resolved IP
   at dial time, re-run on every redirect hop.
3. **Path-traversal-safe object keys** — pure stdlib lexical validation.

`Stripper` + `Fetcher` are fx-provided; `CleanKey` / `MustBeLocal` are pure
package functions (also used by the local storage backend).

## API

```go
// fx-wired
safety.Module                              // provides Stripper + Fetcher
type Stripper interface {
    Strip(ctx, src io.Reader, detectedType string) (io.Reader, string, error)
}
type Fetcher interface {
    Fetch(ctx, rawURL string) (body io.ReadCloser, contentType string, err error)
}

// pure — no injection
safety.CleanKey(key string, maxLen int) (string, error) // normalize + validate
safety.MustBeLocal(key string) error                    // lexical gate
```

Sentinel errors: `ErrUnsupportedType`, `ErrImageTooLarge` (strip);
`ErrBlockedAddress`, `ErrTooLarge`, `ErrBadScheme` (fetch); `ErrUnsafeKey` (keys).

## Why these choices (per concern)

- **Strip = stdlib re-encode, no dep.** `image/jpeg` and `image/png` emit no
  ancillary metadata, so a decode→re-encode round-trip drops it all. Every
  reviewed stripping lib was stale (`go-oss` 2018, JPEG-only), vuln-flagged
  (`rwcarlsen/goexif` GO-2025-3598), or a parser not a stripper (`dsoprea`).
  JPEG orientation is the one nuance: a minimal pure-Go EXIF Orientation read
  (`internal/exif`) bakes rotation into pixels before stripping so phone photos
  stay upright. See ADR-0008.
- **SSRF = `code.dny.dev/ssrf`.** It exposes a `net.Dialer.Control` hook, so it
  composes **under** the framework's instrumented transport instead of replacing
  the client. Deny set is auto-generated from the IANA Special-Purpose
  Registries (loopback/private/link-local/CGNAT/ULA/NAT64), which hand-rolled
  `net.IP.IsPrivate` checks miss. Rejected `doyensec/safeurl` (whole-client
  wrapper that panics on a custom transport Dial); `mccutchen/safedialer`
  (hand-maintained prefixes, more drift). See ADR-0008.
- **Keys = stdlib only.** `path.Clean` + `filepath.IsLocal` (Go 1.20+) plus
  explicit rejection of backslashes, control/null bytes, trailing space/dot, and
  Windows device names (`CON`/`NUL`/...) **regardless of host OS**, so a key
  validated on Linux stays safe if a backend later opens it on Windows. The
  local-disk backend should additionally enforce with `os.Root` (Go 1.24+).

## Notes

- **Deny-by-default media types.** Strip accepts only `image/jpeg|jpg|png|gif`.
  SVG/PDF/Office are never "stripped" — reject by content-type upstream (SVG is
  an XSS/SSRF vector and must never be served inline). WEBP/AVIF/TIFF need
  govips (CGO) and are out of scope.
- **Re-encode is lossy.** JPEG→JPEG degrades quality and changes size; set
  `auto_orient=false` to skip the orientation bake.
- **Decode-bomb guard runs first.** `image.DecodeConfig` + `max_pixels` is
  checked *before* the full `image.Decode` so a tiny file declaring huge
  dimensions errors instead of OOMing.
- **`Fetcher` is the only sanctioned URL-fetch entry point.** A default
  `http.Client` elsewhere bypasses the SSRF guard. `allow_private=true` disables
  the guard for trusted internal fetches and logs a warning.
- Config keys live under `storage.safety.*` (see `module.go`).
- No `init()`, no `fx.Lifecycle`: the guard + client hold no goroutines or open
  connections at rest. A future IANA-prefix-refresh ticker would wire via
  `OnStart`/`OnStop`.
```
storage.safety.strip.auto_orient    bool     (default true)
storage.safety.strip.jpeg_quality   int      (default 85)
storage.safety.strip.max_pixels     int      (default 40000000)   # ~40 MP
storage.safety.fetch.max_bytes      int64    (default 33554432)   # 32 MiB
storage.safety.fetch.timeout        duration (default 15s)
storage.safety.fetch.allowed_schemes []string (default ["https"])
storage.safety.fetch.allow_hosts    []string (default [])
storage.safety.fetch.allow_private  bool     (default false)
storage.safety.fetch.max_redirects  int      (default 3)
storage.safety.keys.max_len         int      (default 1024)
```
