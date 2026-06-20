# Agent guide — media/img/

Image processing (resize, convert, optimize, info) backed by libvips via govips
(CGO). Direct-import constructor — **no fx wiring**; apps build a `Processor` and
share it. The `pipeline/` subpackage adds the fx-wired HTTP handler on top.

## API

```go
p, err := img.NewProcessor(img.Options{}) // libvips threads = NumCPU by default
defer p.Close()                            // releases libvips (vips.Shutdown)

out, err := p.Resize(ctx, src, 800, 600, img.ResizeOptions{Fit: "cover", Quality: 85})
out, err  = p.Convert(ctx, src, img.FormatWEBP, 85)
out, err  = p.Optimize(ctx, src)           // strip metadata, re-encode smaller
w, h, f, err := p.Info(ctx, src)           // dims + format, no full decode
```

Formats: `FormatJPEG`, `FormatPNG`, `FormatWEBP`, `FormatAVIF`, `FormatGIF`,
`FormatTIFF`. `*Processor` is safe to reuse across requests.

## Why govips

- libvips is the fastest image pipeline (low memory, parallel) of the Go-bindable
  libs; govips is the maintained CGO binding with format breadth (AVIF/WEBP/TIFF).

## Notes

- **CGO-gated, own go.mod sub-module.** The shipped `NewProcessor` is a stub that
  returns `ErrCGORequired`; activate the real backend per the package doc (drop
  `//go:build ignore` from `impl_govips.go`, `go get github.com/davidbyttow/govips/v2`,
  `go mod tidy`). Requires `libvips-dev` (apt) / `vips` (brew) installed.
- `Close()` calls `vips.Shutdown()` — call exactly once at app teardown, not per request.
- Decoding untrusted bytes: libvips is the trust boundary; cap input size upstream.
