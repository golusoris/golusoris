// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

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
	"os"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

const (
	defaultRenderTimeout = 30 * time.Second
	// minLaunchTimeout covers a cold start: Chrome reads about 250 MB from disk
	// before its first tab answers, so 60s tolerates cold reads down to ~4 MB/s.
	minLaunchTimeout = 60 * time.Second
	// chromeOutputLimit bounds the browser output a launch error carries.
	chromeOutputLimit = 4 << 10
	// chromedpBackstop scales chromedp's URL-read and dial timers past
	// LaunchTimeout so they never cut a launch short; their defaults are 20s and 10s.
	chromedpBackstop = 2
)

// Options configures the PDF renderer.
type Options struct {
	// Timeout is the per-operation deadline (default: 30s).
	Timeout time.Duration
	// NoSandbox disables Chrome's sandbox (required in some Docker environments).
	NoSandbox bool
	// DisableGPU passes --disable-gpu to Chrome (common in headless CI).
	DisableGPU bool
	// ChromePath overrides CHROME_PATH, CHROMIUM_PATH, and PATH discovery.
	ChromePath string
	// LaunchTimeout bounds browser start-up, separately from Timeout
	// (default: the larger of Timeout and 60s).
	LaunchTimeout time.Duration
}

func (o Options) withDefaults() Options {
	if o.Timeout == 0 {
		o.Timeout = defaultRenderTimeout
	}
	if o.LaunchTimeout == 0 {
		o.LaunchTimeout = max(o.Timeout, minLaunchTimeout)
	}
	return o
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
	ctx    context.Context //nolint:containedctx // chromedp requires the persistent browser context as its session handle.
	cancel context.CancelFunc
	opts   Options
}

// NewRenderer creates and starts a headless Chrome instance.
// Call [Renderer.Close] when done to free the browser process.
// Start-up is bounded by [Options.LaunchTimeout]; a failed launch reports the
// tail of Chrome's output.
func NewRenderer(opts Options) (*Renderer, error) {
	opts = opts.withDefaults()
	output := &outputTail{}
	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), allocatorOptions(opts, output)...)
	ctx, ctxCancel := chromedp.NewContext(allocCtx, chromedp.WithBrowserOption(
		chromedp.WithDialTimeout(chromedpBackstop*opts.LaunchTimeout),
	))

	// Merge cancels: closing allocCtx also kills ctxCancel.
	var cancelOnce sync.Once
	combined := func() {
		cancelOnce.Do(func() {
			ctxCancel()
			cancel()
		})
	}
	startupCtx, cancelStartup := context.WithTimeout(context.Background(), opts.LaunchTimeout)
	stopStartup := context.AfterFunc(startupCtx, combined)
	err := chromedp.Run(ctx)
	startupTimedOut := !stopStartup()
	cancelStartup()
	if err != nil || startupTimedOut {
		combined()
		if startupTimedOut {
			err = fmt.Errorf("%w after %s", context.DeadlineExceeded, opts.LaunchTimeout)
		}
		return nil, launchError(err, output.String())
	}

	return &Renderer{ctx: ctx, cancel: combined, opts: opts}, nil
}

func allocatorOptions(opts Options, output *outputTail) []chromedp.ExecAllocatorOption {
	allocOpts := chromedp.DefaultExecAllocatorOptions[:]
	allocOpts = append(allocOpts,
		chromedp.Headless,
		chromedp.WSURLReadTimeout(chromedpBackstop*opts.LaunchTimeout),
		chromedp.CombinedOutput(output),
	)
	if opts.NoSandbox {
		allocOpts = append(allocOpts, chromedp.NoSandbox)
	}
	if opts.DisableGPU {
		allocOpts = append(allocOpts, chromedp.DisableGPU)
	}
	if executable := chromeExecutablePath(opts); executable != "" {
		allocOpts = append(allocOpts, chromedp.ExecPath(executable))
	}
	return allocOpts
}

// launchError keeps Chrome's own words next to the failure; a crash report from
// chromedp already embeds them, and silence means Chrome never reached logging.
func launchError(err error, output string) error {
	switch {
	case output == "":
		return fmt.Errorf("pdf: launch chrome: %w; chrome printed no output", err)
	case strings.Contains(err.Error(), output):
		return fmt.Errorf("pdf: launch chrome: %w", err)
	default:
		return fmt.Errorf("pdf: launch chrome: %w; chrome output: %s", err, output)
	}
}

// outputTail keeps the most recent chromeOutputLimit bytes of browser output.
type outputTail struct {
	mu   sync.Mutex
	buf  [chromeOutputLimit]byte
	next int
	full bool
}

// Write implements [io.Writer]; it never fails so chromedp keeps draining Chrome.
func (o *outputTail) Write(p []byte) (int, error) {
	written := len(p)
	if len(p) > len(o.buf) {
		p = p[len(p)-len(o.buf):]
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	head := copy(o.buf[o.next:], p)
	copy(o.buf[:], p[head:])
	o.full = o.full || o.next+len(p) >= len(o.buf)
	o.next = (o.next + len(p)) % len(o.buf)
	return written, nil
}

func (o *outputTail) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.full {
		return strings.TrimSpace(string(o.buf[:o.next]))
	}
	return strings.TrimSpace(string(o.buf[o.next:]) + string(o.buf[:o.next]))
}

func chromeExecutablePath(opts Options) string {
	if opts.ChromePath != "" {
		return opts.ChromePath
	}
	if executable := os.Getenv("CHROME_PATH"); executable != "" {
		return executable
	}
	return os.Getenv("CHROMIUM_PATH")
}

// Close shuts down the headless Chrome process.
func (r *Renderer) Close() { r.cancel() }

// RenderURL navigates to url and returns the page as a PDF byte slice.
func (r *Renderer) RenderURL(ctx context.Context, url string, opts RenderOptions) ([]byte, error) {
	if url == "" {
		return nil, errors.New("pdf: url is required")
	}
	return r.render(ctx, url, "url "+url, opts)
}

// RenderHTML loads raw HTML content and returns the page as a PDF byte slice.
// The HTML is loaded via a data: URL so no HTTP server is needed.
func (r *Renderer) RenderHTML(ctx context.Context, html string, opts RenderOptions) ([]byte, error) {
	if html == "" {
		return nil, errors.New("pdf: html is required")
	}
	// data: URLs bypass the need for a running HTTP server.
	dataURL := "data:text/html," + url.PathEscape(html)
	return r.render(ctx, dataURL, "html", opts)
}

func (r *Renderer) render(ctx context.Context, target, description string, opts RenderOptions) ([]byte, error) {
	taskCtx, cancel := r.taskContext(ctx)
	defer cancel()

	var buf []byte
	if err := chromedp.Run(taskCtx,
		chromedp.Navigate(target),
		chromedp.ActionFunc(func(ac context.Context) error {
			var err error
			buf, _, err = opts.params().Do(ac)
			if err != nil {
				return fmt.Errorf("pdf: print page: %w", err)
			}
			return nil
		}),
	); err != nil {
		return nil, fmt.Errorf("pdf: render %s: %w", description, err)
	}
	return buf, nil
}

// taskContext gives each render its own tab on the persistent browser while
// honoring both the caller's cancellation and the operation timeout.
func (r *Renderer) taskContext(ctx context.Context) (context.Context, context.CancelFunc) {
	targetCtx, cancelTarget := chromedp.NewContext(r.ctx)
	taskCtx, cancelTask := context.WithTimeout(targetCtx, r.opts.Timeout)
	stopCaller := context.AfterFunc(ctx, cancelTask)
	return taskCtx, func() {
		stopCaller()
		cancelTask()
		cancelTarget()
	}
}
