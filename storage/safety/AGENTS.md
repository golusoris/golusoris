<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — storage/safety/

Hardens user uploads before they reach a `storage.Bucket`. Four independent
concerns, **security-critical (85% coverage gate)**:

1. **Metadata stripping** — drops EXIF/GPS/XMP/text chunks by re-encoding the
   raster image through stdlib (`image/jpeg`, `image/png`, `image/gif`). No dep.
2. **SSRF-guarded fetch-by-URL** — `code.dny.dev/ssrf` validates the resolved IP
   at dial time, re-run on every redirect hop.
3. **Path-traversal-safe object keys** — pure stdlib lexical validation.
4. **Magic-byte content-type detection** — `h2non/filetype` sniffs the real
   type from content, with a declared-vs-sniffed mismatch check callers can
   run against a client-supplied `Content-Type`.

`Stripper` + `Fetcher` are fx-provided; `CleanKey` / `MustBeLocal` / `Detect` /
`DetectBytes` / `CheckDeclaredType` are pure package functions (also used by
the local storage backend).

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

safety.Detect(ctx, r io.Reader, maxHeaderBytes int) (Detection, error) // scalar-bounded sniff
safety.DetectBytes(buf []byte) (Detection, error)                     // sniff an already-read buffer
safety.CheckDeclaredType(got Detection, declared string) error        // sniff-vs-declared mismatch
type Detection struct {
    MIME, Extension string
    Category        Category // image/video/audio/font/archive/document/application/unknown
    Matched         bool
}
```

Sentinel errors: `ErrUnsupportedType`, `ErrImageTooLarge` (strip);
`ErrBlockedAddress`, `ErrTooLarge`, `ErrBadScheme` (fetch); `ErrUnsafeKey` (keys);
`ErrEmptyInput`, `ErrTypeMismatch` (detect).

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
- **Detect = `h2non/filetype`.** Assigned by the fleet-demand sprint (see
  `docs/FLEET_GO_DEMAND.md`, item 1) as the dependency VMAFx/vmafx already
  carries, so this gap-fill reuses a fleet-wide dependency instead of adding a
  second magic-byte library (`gabriel-vasile/mimetype` is the more actively
  maintained alternative but would duplicate coverage `filetype` already
  provides for the one real consumer). Detection is a pure, in-memory byte
  match — no dial, no decode — so it does not need fx injection; `Detect`
  reads a caller-bounded header off a `Reader`, `DetectBytes` sniffs bytes
  already in hand.

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
- **`Detect`'s read bound is always enforced.** Unlike `CleanKey`'s optional
  `maxLen` (0 = unbounded), `maxHeaderBytes <= 0` falls back to a fixed 8192
  default rather than disabling the cap — an arbitrary `io.Reader` must never
  be drained without a scalar bound (HISS-02). A short read (the source has
  fewer bytes than the bound) is not an error; only zero bytes is
  (`ErrEmptyInput`).
- **`Category` mirrors `h2non/filetype`'s own matcher-family grouping
  verbatim**, including its one surprising choice: PDF is filed under the
  library's `Archive` map upstream, not `Document`. Not overridden, so
  detection stays byte-for-byte identical to the underlying library.
- **`CheckDeclaredType` only ever flags an actual disagreement.** An empty
  declared type or an unmatched `Detection` (many valid formats — JSON, plain
  text, SVG — carry no magic number) returns `nil`, not a guess.
- Config keys live under `storage.safety.*` (see `module.go`).
- No `init()`, no `fx.Lifecycle`: the guard + client hold no goroutines or open
  connections at rest. A future IANA-prefix-refresh ticker would wire via
  `OnStart`/`OnStop`. `Detect` needs neither — it is a pure function.

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
storage.safety.detect.max_header_bytes int  (default 8192)
```
