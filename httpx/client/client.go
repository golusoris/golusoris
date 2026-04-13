// Package client builds outbound [*http.Client] instances with retry,
// circuit-breaker, and OTel instrumentation.
//
// Apps calling third-party services should use [New] (with service-specific
// options) instead of zero-value http.Client — the defaults add resiliency
// that's easy to forget to plumb through.
//
// Layering (outer → inner):
//
//	circuit-breaker -> retry -> otelhttp -> stdlib transport
//
// Circuit-breaker outermost = when the breaker is open, we short-circuit
// without even entering the retry loop. OTel inside the retry layer = each
// retry gets its own span, so failures are visible.
package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	retryablehttp "github.com/hashicorp/go-retryablehttp"
	"github.com/sony/gobreaker/v2"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// Options configures a single [*http.Client] instance. Every field has a
// sensible default; zero value is usable.
type Options struct {
	// Name identifies the client in circuit-breaker state-change logs + OTel
	// span scopes. Defaults to "golusoris.httpx.client".
	Name string

	// Timeout caps a single request (including redirects + body read).
	// 0 falls back to 30s.
	Timeout time.Duration

	// Retry configures the retry policy. Zero value disables retries.
	Retry RetryOptions

	// Breaker configures the circuit breaker. Zero Max disables the breaker.
	Breaker BreakerOptions

	// TracerProvider supplies OTel tracing. nil falls back to
	// otel.GetTracerProvider() (no-op unless the app wires a real one).
	TracerProvider trace.TracerProvider

	// Logger is used for circuit-breaker state-change logs + retry backoff
	// warnings. nil falls back to slog.Default().
	Logger *slog.Logger
}

// RetryOptions tunes retryablehttp. Max == 0 disables retries.
type RetryOptions struct {
	Max     int           // max retry attempts (default 0 = no retries)
	Wait    time.Duration // initial backoff (default 500ms)
	MaxWait time.Duration // max backoff cap (default 10s)
}

// BreakerOptions tunes the circuit breaker. Max == 0 disables the breaker.
type BreakerOptions struct {
	Max     uint32        // consecutive failures to trip (default 0 = disabled)
	OpenFor time.Duration // open-state duration (default 30s)
	HalfMax uint32        // max requests in half-open (default 1)
}

// New constructs a *http.Client with the configured retry/breaker/OTel stack.
func New(opts Options) *http.Client {
	name := opts.Name
	if name == "" {
		name = "golusoris.httpx.client"
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	tp := opts.TracerProvider
	if tp == nil {
		tp = otel.GetTracerProvider()
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// Innermost: stdlib transport wrapped by otelhttp.
	base := otelhttp.NewTransport(http.DefaultTransport,
		otelhttp.WithTracerProvider(tp),
	)

	// Middle: retry layer.
	var transport http.RoundTripper = base
	if opts.Retry.Max > 0 {
		rc := retryablehttp.NewClient()
		rc.HTTPClient = &http.Client{Transport: base, Timeout: timeout}
		rc.RetryMax = opts.Retry.Max
		rc.RetryWaitMin = valOrDefault(opts.Retry.Wait, 500*time.Millisecond)
		rc.RetryWaitMax = valOrDefault(opts.Retry.MaxWait, 10*time.Second)
		rc.Logger = slogRetryLogger{logger: logger}
		transport = &retryTransport{rc: rc}
	}

	// Outermost: circuit breaker.
	if opts.Breaker.Max > 0 {
		cb := gobreaker.NewCircuitBreaker[*http.Response](gobreaker.Settings{ //nolint:bodyclose // response is returned to caller
			Name:        name,
			MaxRequests: valOrDefaultU32(opts.Breaker.HalfMax, 1),
			Timeout:     valOrDefault(opts.Breaker.OpenFor, 30*time.Second),
			ReadyToTrip: func(c gobreaker.Counts) bool {
				return c.ConsecutiveFailures >= opts.Breaker.Max
			},
			OnStateChange: func(n string, from, to gobreaker.State) {
				logger.Warn("httpx/client: breaker state change",
					slog.String("name", n),
					slog.String("from", from.String()),
					slog.String("to", to.String()),
				)
			},
		})
		transport = &breakerTransport{next: transport, cb: cb}
	}

	return &http.Client{Transport: transport, Timeout: timeout}
}

// retryTransport adapts *retryablehttp.Client to http.RoundTripper so the
// circuit breaker sees a uniform interface.
type retryTransport struct{ rc *retryablehttp.Client }

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rreq, err := retryablehttp.FromRequest(req)
	if err != nil {
		return nil, fmt.Errorf("httpx/client: wrap request for retry: %w", err)
	}
	resp, err := t.rc.Do(rreq)
	if err != nil {
		return nil, fmt.Errorf("httpx/client: retry: %w", err)
	}
	return resp, nil
}

// breakerTransport runs the inner RoundTrip inside the circuit breaker. 5xx
// and network errors count as failures; 4xx do not.
type breakerTransport struct {
	next http.RoundTripper
	cb   *gobreaker.CircuitBreaker[*http.Response]
}

func (t *breakerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.cb.Execute(func() (*http.Response, error) {
		r, rtErr := t.next.RoundTrip(req) //nolint:bodyclose // caller closes
		if rtErr != nil {
			return nil, fmt.Errorf("httpx/client: round trip: %w", rtErr)
		}
		if r.StatusCode >= 500 {
			// Count 5xx as failure but still return the response so the
			// caller can inspect it.
			return r, errServerError(r.StatusCode)
		}
		return r, nil
	})
	if err != nil && !errors.Is(err, errServerErrorSentinel) {
		// Breaker open or network error.
		if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
			return nil, fmt.Errorf("httpx/client: circuit open: %w", err)
		}
		return nil, fmt.Errorf("httpx/client: round trip: %w", err)
	}
	// 5xx with response: drop our sentinel, return the response.
	return resp, nil
}

// errServerErrorSentinel is returned as the cause of a wrapped 5xx error so
// breakerTransport can distinguish "real failure that counts" from
// "sentinel wrapped for counting, but caller still wants the response".
var errServerErrorSentinel = errors.New("httpx/client: 5xx response")

func errServerError(code int) error {
	return fmt.Errorf("%w: status %d", errServerErrorSentinel, code)
}

// slogRetryLogger adapts *slog.Logger to retryablehttp.Logger.
type slogRetryLogger struct{ logger *slog.Logger }

func (l slogRetryLogger) Error(msg string, keys ...any) { l.logger.Error(msg, keys...) }
func (l slogRetryLogger) Info(msg string, keys ...any)  { l.logger.Info(msg, keys...) }
func (l slogRetryLogger) Debug(msg string, keys ...any) { l.logger.Debug(msg, keys...) }
func (l slogRetryLogger) Warn(msg string, keys ...any)  { l.logger.Warn(msg, keys...) }

func valOrDefault(v, d time.Duration) time.Duration {
	if v == 0 {
		return d
	}
	return v
}

func valOrDefaultU32(v, d uint32) uint32 {
	if v == 0 {
		return d
	}
	return v
}

// Drain reads + closes resp.Body so the underlying connection is returned to
// the pool. Call this when you've read what you need and want the connection
// reused (e.g. after a HEAD, or after an error early-return from a JSON
// decoder).
func Drain(ctx context.Context, resp *http.Response) {
	_ = ctx
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}
