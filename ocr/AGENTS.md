# Agent guide — ocr/

Extracts text from images using Tesseract via
[gosseract](https://github.com/otiai10/gosseract) (**CGO**). Separate Go module
(`github.com/golusoris/golusoris/ocr`) so its CGO/system-dep weight doesn't
pull into the core build.

Ships a no-op stub by default: `NewReader` returns `ErrCGORequired` until the
real implementation is activated. There is no fx module — construct a `Reader`
directly.

## Key API

| Symbol | Purpose |
|---|---|
| `ocr.NewReader(Options)` | build a `Reader` (stub until activated) |
| `ocr.Reader` | `Read(ctx, bytes)`, `ReadFile(ctx, path)`, `Close()` |
| `ocr.Options` | `Language` (`"eng"`, `"eng+fra"`), `TessdataPrefix`, `AllowList` |
| `ocr.ErrCGORequired` | returned when the CGO impl isn't compiled in |

## Activate the real implementation

```
# system deps (Debian/Ubuntu):
apt-get install libtesseract-dev tesseract-ocr-eng libleptonica-dev
# macOS: brew install tesseract

# 1. remove `//go:build ignore` from ocr/impl_gosseract.go
# 2. go get github.com/otiai10/gosseract/v2
# 3. go mod tidy
```

## Usage

```go
r, err := ocr.NewReader(ocr.Options{Language: "eng"})
if err != nil { return err } // ErrCGORequired if not activated
defer r.Close()
text, err := r.Read(ctx, imageBytes)
```

## Don't

- Don't add gosseract to the root module — keep it in this nested module so
  core builds stay CGO-free.
- Don't share one `Reader` across goroutines — the Tesseract client is stateful
  (it holds the current image); make one per worker or guard with a mutex.
- Don't forget `Close()` — the client wraps native resources that leak otherwise.
- Don't OCR untrusted/huge images without a bound — Tesseract is CPU-heavy; run
  it off the request path (e.g. a `jobs/` worker) with a timeout.
