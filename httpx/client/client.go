// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

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
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	retryablehttp "github.com/hashicorp/go-retryablehttp"
	"github.com/sony/gobreaker/v2"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"github.com/golusoris/golusoris/core/validate"
)

const (
	defaultRequestTimeout = 30 * time.Second
	defaultRetryWait      = 500 * time.Millisecond
	defaultRetryMaxWait   = 10 * time.Second
	defaultBreakerOpenFor = 30 * time.Second
	defaultDrainTimeout   = 5 * time.Second
	maxDrainBytes         = 1 << 20
)

// Options configures a single [*http.Client] instance. Every field has a
// sensible default; zero value is usable.
type Options struct {
	// Name identifies the client in circuit-breaker state-change logs + OTel
	// span scopes. Defaults to "golusoris.httpx.client".
	Name string

	// Timeout caps a single request (including redirects + body read).
	// Non-positive values fall back to 30s.
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

	// TLSConfig sets TLS on a clone of http.DefaultTransport. A core/tlsx
	// Reloader's ClientConfig keeps the client certificate current. Ignored
	// when Transport is set.
	TLSConfig *tls.Config

	// Transport replaces the innermost transport; otelhttp, retry, and the
	// breaker still wrap it. nil uses a private clone of http.DefaultTransport.
	Transport http.RoundTripper
}

// RetryOptions tunes retryablehttp. Max == 0 disables retries.
type RetryOptions struct {
	Max         int           // max retry attempts (default 0 = no retries)
	Wait        time.Duration // initial backoff (non-positive defaults to 500ms)
	MaxWait     time.Duration // backoff cap (non-positive defaults to 10s)
	AllowUnsafe bool          // retry POST/PATCH without an Idempotency-Key
}

// ErrBodyNotReplayable means retries were enabled for a request whose body
// cannot be recreated through [http.Request.GetBody].
var ErrBodyNotReplayable = errors.New("httpx/client: request body is not replayable")

// ErrBodyTooLarge reports a response body larger than its caller-selected cap.
var ErrBodyTooLarge = errors.New("httpx/client: response body exceeds byte cap")

// BreakerOptions tunes the circuit breaker. Max == 0 disables the breaker.
type BreakerOptions struct {
	Max     uint32        // consecutive failures to trip (default 0 = disabled)
	OpenFor time.Duration // open duration (non-positive defaults to 30s)
	HalfMax uint32        // max requests in half-open (default 1)
}

type resolvedOptions struct {
	name           string
	timeout        time.Duration
	tracerProvider trace.TracerProvider
	logger         *slog.Logger
}

func resolveOptions(opts Options) resolvedOptions {
	resolved := resolvedOptions{
		name:           opts.Name,
		timeout:        valOrDefault(opts.Timeout, defaultRequestTimeout),
		tracerProvider: opts.TracerProvider,
		logger:         opts.Logger,
	}
	if resolved.name == "" {
		resolved.name = "golusoris.httpx.client"
	}
	if validate.IsNil(resolved.tracerProvider) {
		resolved.tracerProvider = otel.GetTracerProvider()
	}
	if resolved.logger == nil {
		resolved.logger = slog.Default()
	}
	return resolved
}

// New constructs a *http.Client with the configured retry/breaker/OTel stack.
func New(opts Options) *http.Client {
	resolved := resolveOptions(opts)

	// Innermost: stdlib (or caller) transport wrapped by otelhttp.
	base := otelhttp.NewTransport(
		innerTransport(opts),
		otelhttp.WithTracerProvider(resolved.tracerProvider),
	)

	// Middle: retry layer.
	var transport http.RoundTripper = base
	if opts.Retry.Max > 0 {
		rc := retryablehttp.NewClient()
		rc.HTTPClient = &http.Client{
			Transport: base,
			Timeout:   resolved.timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		rc.RetryMax = opts.Retry.Max
		rc.RetryWaitMin = valOrDefault(opts.Retry.Wait, defaultRetryWait)
		rc.RetryWaitMax = valOrDefault(opts.Retry.MaxWait, defaultRetryMaxWait)
		rc.ErrorHandler = retryablehttp.PassthroughErrorHandler
		rc.Logger = slogRetryLogger{logger: resolved.logger}
		transport = &retryTransport{rc: rc, next: base, allowUnsafe: opts.Retry.AllowUnsafe}
	}

	// Outermost: circuit breaker.
	if opts.Breaker.Max > 0 {
		cb := gobreaker.NewCircuitBreaker[*http.Response](gobreaker.Settings{ //nolint:bodyclose // response is returned to caller
			Name:        resolved.name,
			MaxRequests: valOrDefaultU32(opts.Breaker.HalfMax, 1),
			Timeout:     valOrDefault(opts.Breaker.OpenFor, defaultBreakerOpenFor),
			ReadyToTrip: func(c gobreaker.Counts) bool {
				return c.ConsecutiveFailures >= opts.Breaker.Max
			},
			OnStateChange: func(n string, from, to gobreaker.State) {
				resolved.logger.Warn(
					"httpx/client: breaker state change",
					slog.String("name", n),
					slog.String("from", from.String()),
					slog.String("to", to.String()),
				)
			},
		})
		transport = &breakerTransport{next: transport, cb: cb}
	}

	return &http.Client{Transport: transport, Timeout: resolved.timeout}
}

// innerTransport picks the caller's transport, a TLS-configured clone of the
// default transport, or a private clone of the default transport.
func innerTransport(opts Options) http.RoundTripper {
	if !validate.IsNil(opts.Transport) {
		return opts.Transport
	}
	if opts.TLSConfig == nil {
		return ownTransport()
	}
	tr, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		tr = &http.Transport{Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: true}
	}
	clone := tr.Clone()
	clone.TLSClientConfig = opts.TLSConfig
	return clone
}

// CloneBounded returns a shallow client clone whose timeout is always finite.
// A nil source creates a fresh client. Positive source timeouts are preserved;
// otherwise fallback is used, with the package 30-second default when fallback
// is non-positive. An explicit transport, cookie jar, and redirect policy remain
// shared; a missing transport becomes a private clone of http.DefaultTransport.
func CloneBounded(source *http.Client, fallback time.Duration) *http.Client {
	timeout := valOrDefault(fallback, defaultRequestTimeout)
	if source == nil {
		return &http.Client{Timeout: timeout, Transport: ownTransport()}
	}
	clone := *source
	if clone.Timeout <= 0 {
		clone.Timeout = timeout
	}
	if clone.Transport == nil {
		clone.Transport = ownTransport()
	}
	return &clone
}

// ownTransport clones http.DefaultTransport so the client's idle connections
// are its own: any code may call CloseIdleConnections on the process-wide
// transport (httptest.Server.Close does), which breaks requests mid-flight.
func ownTransport() http.RoundTripper {
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		return base.Clone()
	}
	return http.DefaultTransport
}

// ReadAllBounded reads at most maxBytes plus one sentinel byte. Exact-boundary
// bodies succeed; larger bodies return [ErrBodyTooLarge].
func ReadAllBounded(reader io.Reader, maxBytes int64) ([]byte, error) {
	const maxInt64 = int64(^uint64(0) >> 1)
	if validate.IsNil(reader) {
		return nil, errors.New("httpx/client: response body is required")
	}
	if maxBytes <= 0 || maxBytes == maxInt64 {
		return nil, errors.New("httpx/client: positive bounded response size is required")
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("httpx/client: read response body: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%w: limit %d", ErrBodyTooLarge, maxBytes)
	}
	return data, nil
}

func isIdempotentMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace,
		http.MethodPut, http.MethodDelete:
		return true
	default:
		return false
	}
}

// retryTransport adapts *retryablehttp.Client to http.RoundTripper so the
// circuit breaker sees a uniform interface.
type retryTransport struct {
	rc          *retryablehttp.Client
	next        http.RoundTripper
	allowUnsafe bool
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.allowUnsafe && !isIdempotentMethod(req.Method) && strings.TrimSpace(req.Header.Get("Idempotency-Key")) == "" {
		resp, err := t.next.RoundTrip(req)
		if err != nil {
			return resp, fmt.Errorf("httpx/client: round trip without retry: %w", err)
		}
		return resp, nil
	}
	rreq, err := retryRequest(req)
	if err != nil {
		return nil, fmt.Errorf("httpx/client: wrap request for retry: %w", err)
	}
	resp, err := t.rc.Do(rreq)
	if err != nil {
		return nil, fmt.Errorf("httpx/client: retry: %w", err)
	}
	return resp, nil
}

func retryRequest(req *http.Request) (*retryablehttp.Request, error) {
	rreq := &retryablehttp.Request{Request: req}
	if req.Body == nil || req.Body == http.NoBody {
		return rreq, nil
	}
	if req.GetBody == nil {
		closeErr := closeRequestBody(req.Body)
		return nil, errors.Join(ErrBodyNotReplayable, closeErr)
	}

	originalBody := req.Body
	getBody := req.GetBody
	contentLength := req.ContentLength
	bodyFactory := retryablehttp.ReaderFunc(func() (io.Reader, error) {
		body, err := getBody()
		if err != nil {
			return nil, fmt.Errorf("httpx/client: recreate request body: %w", err)
		}
		if body == nil {
			return nil, errors.New("httpx/client: GetBody returned nil body")
		}
		return body, nil
	})
	if err := rreq.SetBody(bodyFactory); err != nil {
		closeErr := closeRequestBody(originalBody)
		return nil, errors.Join(ErrBodyNotReplayable, fmt.Errorf("httpx/client: configure replay body: %w", err), closeErr)
	}
	rreq.ContentLength = contentLength
	if err := closeRequestBody(originalBody); err != nil {
		return nil, err
	}
	return rreq, nil
}

func closeRequestBody(body io.Closer) error {
	if err := body.Close(); err != nil {
		return fmt.Errorf("httpx/client: close original request body: %w", err)
	}
	return nil
}

// breakerTransport runs the inner RoundTrip inside the circuit breaker. 5xx
// and network errors count as failures; 4xx do not.
type breakerTransport struct {
	next http.RoundTripper
	cb   *gobreaker.CircuitBreaker[*http.Response]
}

func (t *breakerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.cb.Execute(func() (*http.Response, error) {
		r, rtErr := t.next.RoundTrip(req)
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
			var closeErr error
			if req.Body != nil {
				closeErr = closeRequestBody(req.Body)
			}
			return nil, errors.Join(fmt.Errorf("httpx/client: circuit open: %w", err), closeErr)
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
	if v <= 0 {
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

// Drain reads at most 1 MiB for at most five seconds, then closes resp.Body.
// Caller cancellation closes the body to interrupt a blocked read. Read and
// close each use at most one goroutine so an uncooperative custom body cannot
// block the caller past the deadline. A fully drained small response can reuse
// its connection; larger responses cannot. Failures are logged at Debug because
// discarded bytes cannot be recovered.
func Drain(ctx context.Context, resp *http.Response) {
	if resp == nil || validate.IsNil(resp.Body) {
		return
	}
	drainCtx, cancel := context.WithTimeout(ctx, defaultDrainTimeout)
	defer cancel()
	drainResult := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainBytes))
		drainResult <- err
	}()
	closeResult := make(chan error, 1)
	stopClose := context.AfterFunc(drainCtx, func() {
		closeResult <- resp.Body.Close()
	})
	var copyErr error
	select {
	case copyErr = <-drainResult:
	case <-drainCtx.Done():
		copyErr = drainCtx.Err()
	}
	if stopClose() {
		go func() {
			closeResult <- resp.Body.Close()
		}()
	}
	var closeErr error
	select {
	case closeErr = <-closeResult:
	case <-drainCtx.Done():
		closeErr = drainCtx.Err()
	}
	if err := errors.Join(copyErr, closeErr); err != nil {
		slog.Default().DebugContext(ctx, "httpx/client: drain response body", slog.Any("err", err))
	}
}
