<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — storage/safety/

Hardens user uploads before they reach `storage.Bucket`. Four independent
concerns, **security-critical (85% coverage gate)**:

1. **Metadata stripping** — drops EXIF/GPS/XMP/text chunks by re-encoding  raster image through stdlib (`image/jpeg`, `image/png`, `image/gif`). No dep.
2. **SSRF-guarded fetch-by-URL** — `code.dny.dev/ssrf` validates resolved IP
 at dial time. scheme + host allowlists re-run every redirect hop.
3. **Path-traversal-safe object keys** — pure stdlib lexical validation.
4. **Magic-byte content-type detection** — `h2non/filetype` sniffs real
 type from content, with declared-vs-sniffed mismatch check callers can
 run against client-supplied `Content-Type`.

`Stripper` + `Fetcher` are fx-provided; `CleanKey` / `MustBeLocal` / `Detect` /
`DetectBytes` / `CheckDeclaredType` are pure package functions (also used by
local storage backend).

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
safety.CleanKey(key string, maxLen int) (string, error) // canonical validate
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
`ErrBlockedAddress`, `ErrBadScheme` (fetch); `ErrTooLarge` (strip/fetch);
`ErrUnsafeKey` (keys);
`ErrEmptyInput`, `ErrTypeMismatch`, `ErrDeclaredTypeInvalid` (detect).

## Why these choices (per concern)

- **Strip = stdlib re-encode, no dep.** `image/jpeg` and `image/png` emit no
 ancillary metadata, so decode→re-encode round-trip drops it all. Every
 reviewed stripping lib was stale (`go-oss` 2018, JPEG-only), vuln-flagged
 (`rwcarlsen/goexif` GO-2025-3598), or parser not stripper (`dsoprea`).
 JPEG orientation is one nuance: minimal pure-Go EXIF Orientation read
 (`internal/exif`) bakes rotation into pixels before stripping so phone photos
 stay upright. See ADR-0008.
- **SSRF = `code.dny.dev/ssrf`.** It exposes `net.Dialer.Control` hook, so it
 composes **under** framework's instrumented transport instead of replacing
 client. Deny set is auto-generated from IANA Special-Purpose
 Registries (loopback/private/link-local/CGNAT/ULA/NAT64), which hand-rolled
 `net.IP.IsPrivate` checks miss. Rejected `doyensec/safeurl` (whole-client
 wrapper that panics on custom transport Dial); `mccutchen/safedialer`
 (hand-maintained prefixes, more drift). See ADR-0008.
- **Keys = stdlib only.** `path.Clean` + `filepath.IsLocal` (Go 1.20+) verify
  canonical form. Dot, empty, trailing slash, backslash, control/null,
  Windows-invalid/ADS characters, per-component trailing space/dot, and
  device names (`CON`/`NUL`/.) fail regardless of host OS.
 Linux-validated keys remain safe when later opened on Windows. Local-disk
 backends should also enforce with `os.Root` (Go 1.24+).
- **Detect = `h2non/filetype`.** Fleet-demand sprint assigned this dependency;
 see `docs/FLEET_GO_DEMAND.md`, item 1. VMAFx/vmafx already carries it, so this
 gap-fill reuses fleet dependency. `gabriel-vasile/mimetype` is more actively
 maintained but would duplicate `filetype` coverage for one real consumer.
 Detection is pure, in-memory byte
 match — no dial, no decode — so it does not need fx injection; `Detect`
 reads caller-bounded header off `Reader`, `DetectBytes` sniffs bytes
 already in hand.

## Notes

- **Deny-by-default media types.** Strip accepts only `image/jpeg|jpg|png|gif`.
 SVG/PDF/Office are never "stripped" — reject by content-type upstream (SVG is
 XSS/SSRF vector and must never be served inline). WEBP/AVIF/TIFF need
 govips (CGO) and are out of scope.
- **Re-encode is lossy.** JPEG→JPEG degrades quality and changes size; set
 `auto_orient=false` to skip orientation bake.
- **Decode-bomb guards run first.** `max_bytes` bounds encoded buffering.
 `image.DecodeConfig` + division-safe `max_pixels` runs before full decode.
- **GIF animation stays intact.** Bounded preflight sums all frame pixels before
  `gif.DecodeAll`; `gif.EncodeAll` keeps frames, timing, disposal, and loop count
  while dropping comment and text extensions.
- **Strip cancellation starts before source access.** Pre-canceled requests read
  zero bytes; bounded phase-one buffering polls context before and after every
  source read.
- **`Fetcher` is only sanctioned URL-fetch entry point.** default
 `http.Client` elsewhere bypasses SSRF guard. `allow_private=true` disables
 guard for trusted internal fetches and logs warning.
- **Runtime fetch deadlines use `context.WithTimeout`.** They are elapsed-time
  bounds and deliberately do not depend on the injectable domain wall clock.
- **`Detect`'s read bound is always enforced.** Unlike `CleanKey`'s optional
 `maxLen` (0 = unbounded), `maxHeaderBytes <= 0` falls back to fixed 8192
 default rather than disabling cap — arbitrary `io.Reader` must never
 be drained without scalar bound (HISS-02). short read (source has
 fewer bytes than bound) is not error; only zero bytes is
 (`ErrEmptyInput`).
- **`Category` mirrors `h2non/filetype`'s own matcher-family grouping
 verbatim**, including its one surprising choice: PDF is filed under  library's `Archive` map upstream, not `Document`. Not overridden, so
 detection stays byte-for-byte identical to underlying library.
- **`CheckDeclaredType` only ever flags actual disagreement.** empty
 declared type or unmatched `Detection` (many valid formats — JSON, plain
 text, SVG — carry no magic number) returns `nil`, not guess. declared
 type that fails `mime.ParseMediaType` (e.g. trailing `; charset` parameter
 with no value) is explicit mismatch (`ErrDeclaredTypeInvalid`, wrapped in
 `ErrTypeMismatch`), not raw-string comparison fallback — unparseable
 declaration cannot be trusted to carry type it claims. Comparison
 otherwise ignores parameters and is case-insensitive.
- **`Detect`'s buffer allocation follows reader, not bound.** It
 grows to smaller of `maxHeaderBytes` and `r`'s own remaining length when
 `r` reports one (`*bytes.Reader`, `*strings.Reader`, `*bytes.Buffer`);  *read* itself stays hard-capped at `maxHeaderBytes` via `io.LimitReader`
 regardless, so `Reader` with no known length (network body) is never
 drained past bound even though it gets no allocation-size hint.
- Config keys live under `storage.safety.*` (see `module.go`).
- No `init()`, no `fx.Lifecycle`: guard + client hold no goroutines or open
 connections at rest. future IANA-prefix-refresh ticker would wire via
 `OnStart`/`OnStop`. `Detect` needs neither — it is pure function.

```
storage.safety.strip.auto_orient    bool     (default true)
storage.safety.strip.jpeg_quality   int      (default 85)
storage.safety.strip.max_pixels     int      (default 40000000)   # ~40 MP
storage.safety.strip.max_bytes      int64    (default 33554432)   # 32 MiB
storage.safety.fetch.max_bytes      int64    (default 33554432)   # 32 MiB
storage.safety.fetch.timeout        duration (default 15s)
storage.safety.fetch.allowed_schemes []string (default ["https"])
storage.safety.fetch.allow_hosts    []string (default [])
storage.safety.fetch.allow_private  bool     (default false)
storage.safety.fetch.max_redirects  int      (default 3)
storage.safety.keys.max_len         int      (default 1024)
storage.safety.detect.max_header_bytes int  (default 8192)
```
