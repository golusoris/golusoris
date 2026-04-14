// Package pdf generates PDF documents from URLs or raw HTML using a headless
// Chrome browser via chromedp.
//
// Chrome/Chromium must be installed and discoverable (PATH, CHROME_PATH, or
// CHROMIUM_PATH). The renderer spawns one browser process per [Renderer] and
// reuses it across calls.
//
// Usage:
//
//	r, err := pdf.NewRenderer(pdf.Options{Timeout: 30 * time.Second})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer r.Close()
//
//	data, err := r.RenderURL(ctx, "https://example.com", pdf.RenderOptions{
//	    Landscape: false,
//	    Scale:     1.0,
//	})
//
//	data, err = r.RenderHTML(ctx, "<h1>Hello</h1>", pdf.RenderOptions{})
package pdf

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// Options configures the PDF renderer.
type Options struct {
	// Timeout is the per-operation deadline (default: 30s).
	Timeout time.Duration
	// NoSandbox disables Chrome's sandbox (required in some Docker environments).
	NoSandbox bool
	// DisableGPU passes --disable-gpu to Chrome (common in headless CI).
	DisableGPU bool
	// ChromePath overrides the Chrome/Chromium executable path.
	ChromePath string
}

// RenderOptions fine-tunes a single render call.
type RenderOptions struct {
	// Landscape prints in landscape orientation (default: portrait).
	Landscape bool
	// Scale is the CSS scale factor (default: 1.0).
	Scale float64
	// PrintBackground includes background graphics (default: false).
	PrintBackground bool
	// MarginTop/Bottom/Left/Right in centimetres (default: 1.0 each).
	MarginTop, MarginBottom, MarginLeft, MarginRight float64
	// PaperWidth / PaperHeight in centimetres (default: A4 = 21.0 × 29.7).
	PaperWidth, PaperHeight float64
}

func (o RenderOptions) params() *page.PrintToPDFParams {
	p := page.PrintToPDF()
	p = p.WithLandscape(o.Landscape)
	p = p.WithPrintBackground(o.PrintBackground)

	scale := o.Scale
	if scale == 0 {
		scale = 1.0
	}
	p = p.WithScale(scale)

	mt, mb, ml, mr := o.MarginTop, o.MarginBottom, o.MarginLeft, o.MarginRight
	if mt == 0 && mb == 0 && ml == 0 && mr == 0 {
		mt, mb, ml, mr = 1.0, 1.0, 1.0, 1.0
	}
	p = p.WithMarginTop(mt).WithMarginBottom(mb).WithMarginLeft(ml).WithMarginRight(mr)

	pw, ph := o.PaperWidth, o.PaperHeight
	if pw == 0 {
		pw = 21.0 // A4 width in cm
	}
	if ph == 0 {
		ph = 29.7 // A4 height in cm
	}
	p = p.WithPaperWidth(pw / 2.54).WithPaperHeight(ph / 2.54) // chromedp uses inches

	return p
}

// Renderer renders PDFs using a persistent headless Chrome instance.
type Renderer struct {
	ctx    context.Context
	cancel context.CancelFunc
	opts   Options
}

// NewRenderer creates and starts a headless Chrome instance.
// Call [Renderer.Close] when done to free the browser process.
func NewRenderer(opts Options) (*Renderer, error) {
	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Second
	}

	allocOpts := chromedp.DefaultExecAllocatorOptions[:]
	allocOpts = append(allocOpts, chromedp.Headless)
	if opts.NoSandbox {
		allocOpts = append(allocOpts, chromedp.NoSandbox)
	}
	if opts.DisableGPU {
		allocOpts = append(allocOpts, chromedp.DisableGPU)
	}
	if opts.ChromePath != "" {
		allocOpts = append(allocOpts, chromedp.ExecPath(opts.ChromePath))
	}

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	ctx, ctxCancel := chromedp.NewContext(allocCtx)

	// Ping to verify Chrome is reachable.
	pingCtx, pingCancel := context.WithTimeout(ctx, opts.Timeout)
	defer pingCancel()
	if err := chromedp.Run(pingCtx); err != nil {
		ctxCancel()
		cancel()
		return nil, fmt.Errorf("pdf: launch chrome: %w", err)
	}

	// Merge cancels: closing allocCtx also kills ctxCancel.
	combined := func() {
		ctxCancel()
		cancel()
	}

	return &Renderer{ctx: ctx, cancel: combined, opts: opts}, nil
}

// Close shuts down the headless Chrome process.
func (r *Renderer) Close() { r.cancel() }

// RenderURL navigates to url and returns the page as a PDF byte slice.
func (r *Renderer) RenderURL(_ context.Context, url string, opts RenderOptions) ([]byte, error) {
	if url == "" {
		return nil, errors.New("pdf: url is required")
	}
	tCtx, cancel := context.WithTimeout(r.ctx, r.opts.Timeout)
	defer cancel()

	var buf []byte
	if err := chromedp.Run(tCtx,
		chromedp.Navigate(url),
		chromedp.ActionFunc(func(ac context.Context) error {
			var err error
			buf, _, err = opts.params().Do(ac)
			return err
		}),
	); err != nil {
		return nil, fmt.Errorf("pdf: render url %s: %w", url, err)
	}
	return buf, nil
}

// RenderHTML loads raw HTML content and returns the page as a PDF byte slice.
// The HTML is loaded via a data: URL so no HTTP server is needed.
func (r *Renderer) RenderHTML(_ context.Context, html string, opts RenderOptions) ([]byte, error) {
	if html == "" {
		return nil, errors.New("pdf: html is required")
	}
	tCtx, cancel := context.WithTimeout(r.ctx, r.opts.Timeout)
	defer cancel()

	// data: URLs bypass the need for a running HTTP server.
	dataURL := "data:text/html," + url.PathEscape(html)

	var buf []byte
	if err := chromedp.Run(tCtx,
		chromedp.Navigate(dataURL),
		chromedp.ActionFunc(func(ac context.Context) error {
			var err error
			buf, _, err = opts.params().Do(ac)
			return err
		}),
	); err != nil {
		return nil, fmt.Errorf("pdf: render html: %w", err)
	}
	return buf, nil
}
