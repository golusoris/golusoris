# ADR-0016: On-demand image pipeline gated by HMAC signed URLs

- **Status**: Accepted
- **Date**: 2026-06-20
- **Deciders**: @lusoris
- **Tags**: media, img, http, security, ssrf

## Context

`media/img/pipeline` serves resized/re-encoded image variants on demand:
`GET /img/{token}` returns a source image (a `storage.Bucket` key) resized to the
requested width/height/quality/format. The naive shape — `GET /img?key=...&w=...`
— is an **open resize proxy**: an attacker picks arbitrary keys and unbounded
dimensions, turning the endpoint into an SSRF pivot and a decompression-bomb DoS
amplifier (a 100×100 request can decode a 30000×30000 source). SEI CERT and OWASP
ASVS L2 require both surfaces be closed at the framework boundary, per
[principles.md §2.5](../principles.md). The resize itself runs through `media/img`
(libvips via govips, CGO), but the framework must build and serve on runners
without libvips, so the access-control logic cannot depend on CGO.

Two choices are contestable and security-relevant, so they warrant a record:
*how* the endpoint authorizes a request, and *how* it stays buildable without
libvips.

## Decision

We will gate every request with a **self-contained HMAC-SHA256 signed token**:
`base64url(payload) "." base64url(mac)`, where `payload` is a stable canonical
string `escape(key)|w|h|q|format|expiryUnix` and the mac authenticates all of it
under a required `>=16`-byte secret. The handler verifies the mac in constant
time (`hmac.Equal`), rejects expired tokens against an injected `clock.Clock`,
and re-validates the transform against configured bounds (max width/height,
max-pixels, format allowlist) before any decode. The token is self-describing, so
**no server-side state** (DB row, cache) is needed to authorize a request. The
signing/validation/routing logic lives in **CGO-independent files**; the resize
delegates to an injected `img.Processor`, and a stock build (govips not
activated) returns HTTP 415 rather than failing to compile.

## Alternatives considered

| Option | Pros | Cons | Why not chosen |
|---|---|---|---|
| HMAC self-contained signed token (chosen) | Stateless; tamper- + expiry-proof; constant-time verify; no DB lookup on the hot path | Secret rotation invalidates outstanding URLs; token encodes params in the open | Correct shape for a stateless CDN-frontable endpoint; the open params are non-secret by design. |
| Opaque token + server-side param store | Hides params; rotate by deleting rows | Adds a stateful lookup (DB/cache) to every image request; new failure mode | Defeats the point of a cacheable, stateless edge handler. |
| Open proxy + IP allowlist / referer check | Zero token plumbing | Referer is forgeable; allowlists don't bound dimensions; still a decompression-bomb DoS | Not a security control; fails ASVS L2. |
| JWT (HS256) tokens | Standard, libraries exist | Heavier wire form, alg-confusion footguns, claims overkill for 4 ints + a key | Wrong weight class; a bespoke canonical string is smaller and has no alg field to confuse. |
| Per-variant pre-signed `storage.Bucket` URLs | Reuses existing presign | Presigns the *source*, not a *transform*; no resize happens; can't bound output dims | Solves a different problem (raw object access, not derived variants). |

## Consequences

- **Positive**: The endpoint is safe to expose publicly and front with a CDN —
  tokens are unforgeable, expiring, and self-validating with no per-request state.
  Output dimensions are bounded before decode (decompression-bomb guard,
  Power-of-10 rule 2). The access-control path has zero CGO, so signing/validation
  is testable and shippable on a no-libvips runner (≥70% coverage of the non-CGO
  logic; this package lands at 85%).
- **Negative**: Rotating the signing secret invalidates all outstanding URLs at
  once (mitigate with a short `default_ttl` and, if needed, a future
  multi-secret verify list). The canonical-string format is now a compatibility
  surface: changing field order/encoding breaks live tokens, so it is frozen by
  this ADR. The transform params travel in the clear (acceptable — they are not
  secret; the key is URL-escaped so the separator cannot be injected).
- **Neutral / follow-ups**: The real libvips resize is exercised only under the
  `imgvips` build tag (mirrors `media/img`'s govips activation); CI without
  libvips covers the stub (415) path. Secret is required at boot — `New` rejects
  secrets shorter than 16 bytes so an app cannot accidentally ship an
  unauthenticated proxy. A future secret-rotation window (overlapping verify
  keys) wires via config, not `init()`.

## References

- [RFC 2104 — HMAC: Keyed-Hashing for Message Authentication] — token MAC.
- `crypto/hmac.Equal` — constant-time comparison (defeats timing oracle).
- ADR-0008 — upload-side SSRF + decompression hardening (sibling concern on ingest).
- [principles.md §2.5 / §2.6](../principles.md) — security + wire-protocol standards.
- `media/img/pipeline/AGENTS.md` — API, token format, config keys, build tags.
- `media/img/AGENTS.md` — the govips processor this pipeline delegates resize to.
