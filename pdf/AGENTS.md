<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — pdf/

Two concerns, both in separate Go module
(`github.com/golusoris/golusoris/pdf`) to keep heavyweight deps out of core
build:

- **`pdf`** — *generates* PDFs from URL or raw HTML using headless Chrome via
 [chromedp](https://github.com/chromedp/chromedp).
- **`pdf/parse`** — *reads* text, metadata, and page info from existing PDFs
 using [pdfcpu](https://github.com/pdfcpu/pdfcpu) (pure-Go, no CGO).

No fx module — construct `Renderer` directly and `Close()` it.

## Generation (`pdf`)

| Symbol | Purpose |
| --- | --- |
| `pdf.NewRenderer(Options)` | start persistent Chrome; return `*Renderer` |
| `Renderer.RenderURL(ctx, url, RenderOptions)` | navigate + print → PDF bytes |
| `Renderer.RenderHTML(ctx, html, RenderOptions)` | render HTML data URL |
| `Renderer.Close()` | kill the browser process |
| `pdf.Options` | `Timeout`, `LaunchTimeout`, `NoSandbox`, `DisableGPU`, `ChromePath` |
| `pdf.RenderOptions` | layout, scale, background, margins, paper |

Chrome/Chromium must be installed and discoverable via PATH, `CHROME_PATH`,
`CHROMIUM_PATH`, or `Options.ChromePath`. One browser process per `Renderer`,
reused across calls. Each render owns transient tab; concurrent calls stay
page-isolated. Caller cancellation or `Timeout` closes only its tab;
`Renderer.Close` cancels every active render and browser process.
Discovery precedence: `Options.ChromePath` > `CHROME_PATH` > `CHROMIUM_PATH` >
chromedp PATH lookup.

Start-up budget = `LaunchTimeout` (default: max of `Timeout`, 60s), apart from
per-render `Timeout`. Cold Chrome start reads ~250 MB before first tab answers;
slow CI disks need 15-25 s. Budget also caps chromedp URL-read + websocket dial
timers. Launch error carries last 4 KiB of Chrome output, or says Chrome
printed nothing.

```go
r, err := pdf.NewRenderer(pdf.Options{Timeout: 30 * time.Second})
if err != nil { return err }
defer r.Close()
opts := pdf.RenderOptions{PrintBackground: true}
data, err := r.RenderHTML(ctx, "<h1>Invoice</h1>", opts)
```

## Parsing (`pdf/parse`)

```go
info, err := parse.Info(ctx, r, "report.pdf")
err = parse.Validate(ctx, r)
err = parse.Merge(ctx, []string{"a.pdf", "b.pdf"}, "out.pdf")
```

## Don't

- Don't add chromedp/pdfcpu to root module — keep them in this nested
 module so core stays light.
- Don't create `Renderer` per request — it spawns browser; build one,
 reuse it, `Close()` on shutdown.
- Don't render untrusted URLs/HTML without isolation — headless Chrome will
 fetch remote resources and execute JS (SSRF / resource-exhaustion risk). Run
 it sandboxed, on allowlist, off request path.
- Don't set `NoSandbox` outside locked-down container — it removes Chrome
 security boundary.
