# ADR-0008: Upload hardening — strip-by-re-encode and SSRF via dialer Control

- **Status**: Accepted
- **Date**: 2026-06-19
- **Deciders**: @lusoris
- **Tags**: storage, security, ssrf, uploads

## Context

User uploads carry two latent attack surfaces. (1) Image files embed metadata
(EXIF GPS coordinates, camera serials, XMP, PNG text chunks) that leaks PII when
served back — a GDPR / BSI C5 concern per [principles.md §2.5](../principles.md).
(2) Any "fetch this image by URL" feature is a Server-Side Request Forgery
vector: an attacker supplies `http://169.254.169.254/` (cloud metadata) or an
internal `10.x` address and the server fetches it. SEI CERT and OWASP ASVS L2
require both surfaces be closed at the framework boundary, not per-app.

Two architecture choices are contestable and have security implications, so they
warrant a record: *how* to strip metadata, and *how* to enforce SSRF protection
without breaking the framework's existing instrumented HTTP client.

## Decision

We will strip image metadata by **decode → optional orientation-bake →
re-encode through the Go stdlib** (`image/jpeg`, `image/png`, `image/gif`), which
emits no ancillary metadata, rather than parsing and rewriting EXIF blocks. We
will enforce SSRF protection via **`code.dny.dev/ssrf`'s `net.Dialer.Control`
hook**, which composes *under* the framework's OTel-instrumented `http.Transport`
and validates the resolved IP at dial time (re-run per redirect hop), rather than
a whole-client wrapper. Object-key traversal validation stays pure stdlib
(`path.Clean` + `filepath.IsLocal` + explicit cross-OS reserved-name rejection).

## Alternatives considered

| Option | Pros | Cons | Why not chosen |
|---|---|---|---|
| Strip via stdlib re-encode (chosen) | Zero dep; drops *all* metadata categories by construction; pure Go | Lossy for JPEG; raster-only (no WEBP/AVIF without CGO) | Best safety/maintenance trade-off; the lossy re-encode is acceptable and documented. |
| Parse-and-rewrite EXIF (`dsoprea/go-exif`) | Lossless container edit | Heavy transitive tree; parses but does not strip the container; only addresses EXIF, not XMP/PNG-text | Wrong tool: we need stripping, not parsing. |
| `go-oss` / `rwcarlsen/goexif` | Purpose-built | Stale (2018, JPEG-only) / open OSV advisory GO-2025-3598 | Unacceptable in an 85%-coverage security package. |
| SSRF via `ssrf` dialer Control (chosen) | Composes under our transport; IANA-registry-generated deny set; dial-time + per-hop validation | 2023 v0.2.0 tag (low maintenance velocity) | Correct shape and most complete coverage; surface is tiny and regenerable, so a fork is cheap. |
| `doyensec/safeurl` | Newer, maintained | Whole-client wrapper that **panics** if the transport defines a custom Dial — collides with our OTel client | Wrong shape for a framework that ships its own HTTP client. |
| `mccutchen/safedialer` | Same Control shape, pure Go | Deny ranges hand-maintained in source, not IANA-generated | More drift risk for a security gate. |
| Hand-rolled `net.IP.IsPrivate` | No dep | Explicitly not a security primitive (golang/go#79925): misses link-local, CGNAT, IPv4-mapped v6, NAT64, 0.0.0.0; runs post-DNS (rebinding-vulnerable) | Re-implements a registry `ssrf` already maintains, badly. |

## Consequences

- **Positive**: One sanctioned, instrumented fetch path with correct IANA-backed
  deny coverage and per-hop revalidation (defeats DNS rebinding and
  redirect-to-internal). Metadata stripping needs no third-party dep and removes
  every metadata class, not just EXIF. Key validation is cross-OS safe.
- **Negative**: JPEG re-encode is lossy (quality/size change); `auto_orient`
  bakes rotation but the orientation read is a small bespoke EXIF parser we now
  own. Raster scope only: WEBP/AVIF/TIFF require govips (CGO, separate go.mod);
  SVG/PDF/Office are rejected by content-type, never "stripped".
- **Follow-ups**: Pin `code.dny.dev/ssrf` and add it to Renovate; if the prefix
  table drifts, regenerate via `ssrfgen` or fork. All upload-by-URL code MUST
  route through `safety.Fetcher`; any default `http.Client` bypasses the guard.
  A future IANA-prefix-refresh ticker wires via `fx.Lifecycle`, never `init()`.

## References

- [RFC — IANA IPv4/IPv6 Special-Purpose Address Registries] — source of the deny set.
- `code.dny.dev/ssrf` (github.com/daenney/ssrf), MIT, v0.2.0 — dialer Control hook.
- golang/go#79925 — `net.IP.IsPrivate` is not a security primitive.
- OSV GO-2025-3598 — advisory on `rwcarlsen/goexif`.
- [principles.md §2.5 / §2.6](../principles.md) — security + wire-protocol standards.
- `storage/safety/AGENTS.md` — implementation + config keys.
