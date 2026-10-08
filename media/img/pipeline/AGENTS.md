<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# media/img/pipeline

Signed resize route. Injected `img.Processor`. No runtime backend bundled.
Decision: `docs/adr/0016-image-pipeline-signed-urls.md`.

## API

```go
source := pipeline.SourceFromBucket(bucket)
p, err := pipeline.New(opts, processor, source, clk, logger)
tok, err := p.Sign(
    "avatars/u42.png",
    pipeline.Transform{Width: 256, Format: "webp"},
    5*time.Minute,
)
key, transform, err := p.Verify(tok)
mux.Handle("/img/{signed}", p.Handler())
```

`Transform{Width, Height, Quality, Format}`. Zero axis uses configured axis
maximum. Effective pixel count must fit `max_pixels`. Empty format keeps source
encoding; handler inspects rendered bytes for MIME.

## Token format

`base64url(payload).base64url(HMAC-SHA256)`.

- Payload: `escape(key)|w|h|q|format|expiryUnix`.
- Compare: `hmac.Equal`.
- Verify: signature, expiry, all current bounds.

## Handler status mapping

| Status | Cause |
| --- | --- |
| 200 | variant served (correct `Content-Type` + `Cache-Control`) |
| 400 | malformed token / invalid params (`ErrBadToken`, `ErrInvalidParams`) |
| 403 | bad signature or expired |
| 404 | source key not found (`storage.ErrNotFound`) |
| 405 | method outside GET/HEAD; no token, source, or processor work |
| 415 | injected backend reports `img.ErrCGORequired` |
| 500 | source read / resize failure |

Public cache freshness never exceeds signed-token remaining lifetime. Existing
shorter `max-age` stays shorter; `s-maxage` receives same cap.

## Backend

- Runtime: application provides `img.Processor`.
- Lifecycle: provider owns `Processor.Close`.
- Parent `img.NewProcessor`: compatibility stub only. Do not wire it.
- `imgvips` tag: test-only govips adapter; not product activation.

```console
go test -tags imgvips -race ./pipeline/...
```

## fx wiring

`pipeline.Module` provides `*Pipeline` plus named handler
`name:"media.img.pipeline"`.

Required graph:

- `img.Processor`: application backend.
- `storage.Bucket`.
- `clock.Clock`.
- `*config.Config`.
- `*slog.Logger`.
- `New`: rejects nil dependencies with `ErrInvalidDependency`.

```go
fx.New(
    golusoris.Core,
    storage.Module,
    myimage.Module, // provides img.Processor and owns lifecycle
    pipeline.Module,
)
```

## Config (`media.img.pipeline` prefix)

```ini
media.img.pipeline.secret           = "<>=16 bytes>"
media.img.pipeline.allowed_formats  = jpeg,png,webp,gif
media.img.pipeline.max_width       = 4096
media.img.pipeline.max_height      = 4096
media.img.pipeline.max_pixels       = 16777216
media.img.pipeline.max_source_bytes = 33554432
media.img.pipeline.cache_control   = "public, max-age=31536000, immutable"
media.img.pipeline.default_ttl      = 5m
```

## Never

- No secret below 16 bytes.
- No open resize proxy.
- No unbounded source bytes, dimensions, pixels, or TTL.
- No direct MAC compare.
- No `time.Now`; injected clock only.
- No inert default processor in fx graph.
- Source reads check request cancellation before and after every read.
- `storage.Bucket` needs `SourceFromBucket`; `Bucket.Get` also returns metadata.
