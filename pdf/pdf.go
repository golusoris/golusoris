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
//	r, err := pdf.NewRendererContext(ctx, pdf.Options{Timeout: 30 * time.Second})
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

	"github.com/chromedp/cdproto/cdp"
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
	scale := o.Scale
	if scale == 0 {
		scale = 1.0
	}
	mt, mb, ml, mr := o.MarginTop, o.MarginBottom, o.MarginLeft, o.MarginRight
	if mt == 0 && mb == 0 && ml == 0 && mr == 0 {
		mt, mb, ml, mr = 1.0, 1.0, 1.0, 1.0
	}
	pw, ph := o.PaperWidth, o.PaperHeight
	if pw == 0 {
		pw = 21.0 // A4 width in cm
	}
	if ph == 0 {
		ph = 29.7 // A4 height in cm
	}
	return &page.PrintToPDFParams{
		Landscape:       new(o.Landscape),
		PrintBackground: new(o.PrintBackground),
		Scale:           scale,
		MarginTop:       new(mt),
		MarginBottom:    new(mb),
		MarginLeft:      new(ml),
		MarginRight:     new(mr),
		PaperWidth:      pw / 2.54, // chromedp uses inches
		PaperHeight:     ph / 2.54,
	}
}

// Renderer renders PDFs using a persistent headless Chrome instance.
type Renderer struct {
	ctx    context.Context //nolint:containedctx // chromedp requires the persistent browser context as its session handle.
	cancel context.CancelFunc
	opts   Options
}

// NewRendererContext creates and starts a headless Chrome instance whose
// session lives until ctx is done or [Renderer.Close] is called.
// Call [Renderer.Close] when done to free the browser process.
// Start-up is bounded by [Options.LaunchTimeout]; a failed launch reports the
// tail of Chrome's output.
func NewRendererContext(ctx context.Context, opts Options) (*Renderer, error) {
	opts = opts.withDefaults()
	output := &outputTail{}
	allocCtx, cancel := chromedp.NewExecAllocator(ctx, allocatorOptions(opts, output)...)
	browserCtx, ctxCancel := chromedp.NewContext(allocCtx)

	// Merge cancels: closing allocCtx also kills ctxCancel.
	var cancelOnce sync.Once
	combined := func() {
		cancelOnce.Do(func() {
			ctxCancel()
			cancel()
		})
	}
	startupCtx, cancelStartup := context.WithTimeout(ctx, opts.LaunchTimeout)
	stopStartup := context.AfterFunc(startupCtx, combined)
	err := chromedp.Do(browserCtx)
	startupTimedOut := !stopStartup()
	cancelStartup()
	if err != nil || startupTimedOut {
		combined()
		if startupTimedOut {
			err = startupError(startupCtx.Err(), opts.LaunchTimeout)
		}
		return nil, launchError(err, output.String())
	}

	return &Renderer{ctx: browserCtx, cancel: combined, opts: opts}, nil
}

// startupError reports why start-up stopped: the launch deadline, or the
// caller's context ending first.
func startupError(err error, launchTimeout time.Duration) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w after %s", err, launchTimeout)
	}
	return err
}

func allocatorOptions(opts Options, output *outputTail) []chromedp.ExecAllocatorOption {
	allocOpts := chromedp.DefaultExecAllocatorOptions[:]
	allocOpts = append(allocOpts,
		chromedp.Headless,
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

	if err := chromedp.Do(taskCtx, chromedp.Navigate(target)); err != nil {
		return nil, fmt.Errorf("pdf: render %s: %w", description, err)
	}
	buf, err := chromedp.Run(taskCtx, func(ac context.Context, tab *chromedp.Target) ([]byte, error) {
		result, printErr := cdp.Call(ac, tab, page.PrintToPDF, *opts.params())
		if printErr != nil {
			return nil, fmt.Errorf("pdf: print page: %w", printErr)
		}
		return result.Data, nil
	})
	if err != nil {
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
