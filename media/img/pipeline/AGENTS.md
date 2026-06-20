# Agent guide — media/img/pipeline/

On-demand image resize + **signed-URL** serving on top of the `media/img`
govips transforms. An app mounts one handler and hands out short-lived signed
tokens; the endpoint is **not** an open resize proxy (SSRF / decompression-bomb
DoS guard). Sub-package of the `media/img` module (same `go.mod`).

See [ADR-0016](../../../docs/adr/0016-image-pipeline-signed-urls.md) for the
HMAC-token rationale.

## API

```go
p, err := pipeline.New(opts, processor, source, clk, logger) // err if secret < 16 bytes
tok, err := p.Sign("avatars/u42.png", pipeline.Transform{Width: 256, Format: "webp"}, 5*time.Minute)
key, t, err := p.Verify(tok)        // ErrBadToken | ErrBadSignature | ErrExpired | ErrInvalidParams
mux.Handle("/img/{signed}", p.Handler())
```

`Transform{Width, Height, Quality, Format}` — a 0 dimension means "unbounded on
that axis" (aspect ratio preserved); empty `Format` keeps the source format.

## Token format

`base64url(payload) "." base64url(hmac-sha256)`, where `payload` is the stable
canonical string `escape(key)|w|h|q|format|expiryUnix`. The key is URL-escaped so
the `|` separator can never appear inside it. Verification is **constant-time**
(`hmac.Equal`) and re-validates bounds (defense in depth if `Options` tightened
after the token was minted).

## Handler status mapping

| Status | Cause |
|---|---|
| 200 | variant served (correct `Content-Type` + `Cache-Control`) |
| 400 | malformed token / invalid params (`ErrBadToken`, `ErrInvalidParams`) |
| 403 | bad signature or expired (`ErrBadSignature`, `ErrExpired`) — indistinguishable to a prober |
| 404 | source key not found (`storage.ErrNotFound`) |
| 415 | resize backend unavailable (`img.ErrCGORequired` — no libvips) |
| 500 | source read / resize failure |

## CGO + build tags

The **signing, validation, and routing logic is CGO-independent** and lives in
non-CGO files (`sign.go`, `options.go`, `pipeline.go`, `handler.go`,
`format.go`, `module.go`) — it builds and tests on a runner without libvips. The
actual resize delegates to the injected `img.Processor`; the parent
`img.NewProcessor` ships stubbed (returns `img.ErrCGORequired`) until govips is
activated, so on a stock build the handler returns **415** rather than failing.

The real libvips round-trip lives in `resize_vips_test.go`, gated by the
`imgvips` build tag (mirrors `media/img`'s activation pattern):

```
go get github.com/davidbyttow/govips/v2
go test -tags imgvips -race ./pipeline/...
```

## fx wiring

`pipeline.Module` (`golusoris.media.img.pipeline`) provides `*Pipeline` and a
**named** `http.Handler` (`name:"media.img.pipeline"`). It depends on
`storage.Bucket`, `clock.Clock`, `*config.Config`, `*slog.Logger`.

```go
fx.New(
    golusoris.Core,
    storage.Module,   // storage.Bucket
    clock.Module,     // clock.Clock
    pipeline.Module,  // *pipeline.Pipeline + the handler
)
```

The processor is closed on fx stop; **no `init()`** — lifecycle only.

## Config (`media.img.pipeline` prefix)

```
media.img.pipeline.secret          = "<>=16 bytes>"   # REQUIRED; boot fails without it
media.img.pipeline.allowed_formats = jpeg,png,webp,gif  # default: jpeg,png,webp,gif
media.img.pipeline.max_width       = 4096
media.img.pipeline.max_height      = 4096
media.img.pipeline.max_pixels      = 16777216          # 16 MP — decompression-bomb cap
media.img.pipeline.cache_control   = "public, max-age=31536000, immutable"
media.img.pipeline.default_ttl     = 5m                # Sign(ttl<=0) lifetime
```

## Don't

- Don't ship without a real `secret` — `New` rejects secrets shorter than 16
  bytes so an app can't accidentally expose an unauthenticated resize proxy.
- Don't widen `max_*` bounds without thinking about decompression-bomb resizes
  (Power-of-10 rule 2: every allocation is bounded).
- Don't compare MACs with `==`/`bytes.Equal` — always `hmac.Equal` (constant-time).
- Don't reach for `time.Now()` — the expiry clock is injected (`clock.Clock`).
