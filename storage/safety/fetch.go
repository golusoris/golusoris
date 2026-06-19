package safety

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"time"

	"code.dny.dev/ssrf"

	"github.com/golusoris/golusoris/clock"
)

// Fetching errors.
var (
	ErrBlockedAddress = errors.New("storage/safety: address blocked by SSRF guard")
	ErrTooLarge       = errors.New("storage/safety: response exceeds max bytes")
	ErrBadScheme      = errors.New("storage/safety: scheme not allowed")
)

// Fetcher GETs a URL through an SSRF-guarded client. It is the only sanctioned
// fetch-by-URL entry point; a default http.Client elsewhere bypasses the guard.
type Fetcher interface {
	// Fetch GETs rawURL through an SSRF-guarded client (scheme allowlist,
	// dial-time IP validation re-run on every redirect hop) and returns a
	// size-capped body the caller MUST Close.
	Fetch(ctx context.Context, rawURL string) (body io.ReadCloser, contentType string, err error)
}

type fetcher struct {
	opts   FetchOptions
	logger *slog.Logger
	clk    clock.Clock
	client *http.Client
}

// newFetcher builds the SSRF-guarded client eagerly. It holds no goroutines or
// open connections at rest, so no fx.Lifecycle hook is needed.
func newFetcher(opts Options, logger *slog.Logger, clk clock.Clock) (Fetcher, error) {
	f := &fetcher{opts: opts.Fetch, logger: logger, clk: clk}
	guard := ssrf.New()
	dialer := &net.Dialer{Control: guard.Safe}
	if f.opts.AllowPrivate {
		dialer.Control = nil
		logger.Warn("storage/safety: SSRF guard disabled (allow_private=true)")
	}
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	f.client = &http.Client{
		// Client-level backstop in addition to the per-request context deadline
		// in Fetch, so a slow peer can never hang a request past opts.Timeout.
		Timeout:       f.opts.Timeout,
		Transport:     transport,
		CheckRedirect: f.checkRedirect,
	}
	return f, nil
}

// checkRedirect re-validates the scheme on every hop and bounds hop count. The
// SSRF IP check re-runs naturally because the client re-dials per hop.
func (f *fetcher) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= f.opts.MaxRedirects {
		return fmt.Errorf("storage/safety: too many redirects (%d)", len(via))
	}
	return f.checkScheme(req.URL)
}

// checkScheme enforces the scheme allowlist (default https only), blocking
// file/gopher/ftp at parse/redirect before any dial.
func (f *fetcher) checkScheme(u *url.URL) error {
	if !slices.Contains(f.opts.AllowedSchemes, u.Scheme) {
		return fmt.Errorf("%w: %q", ErrBadScheme, u.Scheme)
	}
	return nil
}

// Fetch implements [Fetcher].
func (f *fetcher) Fetch(
	ctx context.Context,
	rawURL string,
) (io.ReadCloser, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, "", fmt.Errorf("storage/safety: parse url: %w", err)
	}
	if err = f.checkScheme(u); err != nil {
		return nil, "", err
	}
	if err = f.checkHost(u); err != nil {
		return nil, "", err
	}
	ctx, cancel := context.WithDeadline(ctx, f.clk.Now().Add(f.opts.Timeout))
	//nolint:bodyclose // resp.Body is owned by the returned cappedBody; caller Closes it.
	resp, err := f.do(ctx, u.String())
	if err != nil {
		cancel()
		return nil, "", err
	}
	// remaining = MaxBytes+1: the extra sentinel byte distinguishes a body that
	// is exactly at the cap (EOF) from one that exceeds it (ErrTooLarge).
	body := &cappedBody{rc: resp.Body, remaining: f.opts.MaxBytes + 1, cancel: cancel}
	return body, resp.Header.Get("Content-Type"), nil
}

// checkHost applies the optional extra host allowlist; an empty list allows any
// host (the SSRF guard still gates the resolved IP at dial time).
func (f *fetcher) checkHost(u *url.URL) error {
	if len(f.opts.AllowHosts) == 0 {
		return nil
	}
	if !slices.Contains(f.opts.AllowHosts, u.Hostname()) {
		return fmt.Errorf("%w: host %q not allowlisted", ErrBlockedAddress, u.Hostname())
	}
	return nil
}

// do issues the GET and maps a dial-time SSRF block to ErrBlockedAddress.
func (f *fetcher) do(ctx context.Context, target string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("storage/safety: build request: %w", err)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		if isBlocked(err) {
			return nil, fmt.Errorf("%w: %s", ErrBlockedAddress, target)
		}
		if errors.Is(err, ErrBadScheme) {
			return nil, ErrBadScheme
		}
		return nil, fmt.Errorf("storage/safety: fetch: %w", err)
	}
	return resp, nil
}

// isBlocked reports whether err originates from the SSRF guard's Control hook.
func isBlocked(err error) bool {
	return errors.Is(err, ssrf.ErrProhibitedIP) ||
		errors.Is(err, ssrf.ErrProhibitedNetwork) ||
		errors.Is(err, ssrf.ErrProhibitedPort) ||
		errors.Is(err, ssrf.ErrInvalidHostPort)
}

// cappedBody enforces MaxBytes on read and cancels the request context on Close.
// It reads one sentinel byte past the cap so a body exactly at MaxBytes returns
// EOF while a body of MaxBytes+1 returns ErrTooLarge.
type cappedBody struct {
	rc        io.ReadCloser
	remaining int64 // bytes still permitted, including one sentinel past the cap
	cancel    context.CancelFunc
}

func (c *cappedBody) Read(p []byte) (int, error) {
	if c.remaining <= 0 {
		return 0, ErrTooLarge
	}
	if int64(len(p)) > c.remaining {
		p = p[:c.remaining]
	}
	n, err := c.rc.Read(p)
	c.remaining -= int64(n)
	if c.remaining <= 0 && err == nil {
		return n, ErrTooLarge
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return n, fmt.Errorf("storage/safety: read body: %w", err)
	}
	return n, err //nolint:wrapcheck // io.EOF must propagate verbatim to readers
}

func (c *cappedBody) Close() error {
	c.cancel()
	if err := c.rc.Close(); err != nil {
		return fmt.Errorf("storage/safety: close body: %w", err)
	}
	return nil
}
