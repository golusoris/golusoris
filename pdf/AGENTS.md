# Agent guide — pdf/

Two concerns, both in a separate Go module
(`github.com/golusoris/golusoris/pdf`) to keep their heavyweight deps out of the
core build:

- **`pdf`** — *generates* PDFs from a URL or raw HTML using headless Chrome via
  [chromedp](https://github.com/chromedp/chromedp).
- **`pdf/parse`** — *reads* text, metadata, and page info from existing PDFs
  using [pdfcpu](https://github.com/pdfcpu/pdfcpu) (pure-Go, no CGO).

No fx module — construct a `Renderer` directly and `Close()` it.

## Generation (`pdf`)

| Symbol | Purpose |
|---|---|
| `pdf.NewRenderer(Options)` | start a persistent headless Chrome, returns `*Renderer` |
| `Renderer.RenderURL(ctx, url, RenderOptions)` | navigate + print → PDF bytes |
| `Renderer.RenderHTML(ctx, html, RenderOptions)` | render raw HTML (via `data:` URL) |
| `Renderer.Close()` | kill the browser process |
| `pdf.Options` | `Timeout`, `NoSandbox`, `DisableGPU`, `ChromePath` |
| `pdf.RenderOptions` | `Landscape`, `Scale`, `PrintBackground`, margins/paper (cm) |

Chrome/Chromium must be installed and discoverable via PATH, `CHROME_PATH`,
`CHROMIUM_PATH`, or `Options.ChromePath`. One browser process per `Renderer`,
reused across calls.

```go
r, err := pdf.NewRenderer(pdf.Options{Timeout: 30 * time.Second})
if err != nil { return err }
defer r.Close()
data, err := r.RenderHTML(ctx, "<h1>Invoice</h1>", pdf.RenderOptions{PrintBackground: true})
```

## Parsing (`pdf/parse`)

```go
info, err := parse.Info(ctx, r, "report.pdf") // Metadata: Pages, Title, Author, ...
err = parse.Validate(ctx, r)
err = parse.Merge(ctx, []string{"a.pdf", "b.pdf"}, "out.pdf")
```

## Don't

- Don't add chromedp/pdfcpu to the root module — keep them in this nested
  module so core stays light.
- Don't create a `Renderer` per request — it spawns a browser; build one,
  reuse it, `Close()` on shutdown.
- Don't render untrusted URLs/HTML without isolation — headless Chrome will
  fetch remote resources and execute JS (SSRF / resource-exhaustion risk). Run
  it sandboxed, on an allowlist, off the request path.
- Don't set `NoSandbox` outside a locked-down container — it removes a Chrome
  security boundary.
