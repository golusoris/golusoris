// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package middleware_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jonboulle/clockwork"
	"go.opentelemetry.io/otel/trace"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/httpx/middleware"
)

type typedNilTracerProvider struct{ trace.TracerProvider }

func TestChainOrder(t *testing.T) {
	t.Parallel()
	var order []string
	mk := func(name string) middleware.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, "in:"+name)
				next.ServeHTTP(w, r)
				order = append(order, "out:"+name)
			})
		}
	}
	h := middleware.Chain(mk("a"), mk("b"), mk("c"))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		order = append(order, "handler")
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	want := []string{"in:a", "in:b", "in:c", "handler", "out:c", "out:b", "out:a"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", order, want)
	}
}

func TestChainIgnoresNilMiddleware(t *testing.T) {
	t.Parallel()
	handler := middleware.Chain(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusTeapot)
	}
}

func TestChainSnapshotsMiddlewareSlice(t *testing.T) {
	t.Parallel()
	called := ""
	middlewares := []middleware.Middleware{
		func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = "original"
				next.ServeHTTP(w, r)
			})
		},
	}
	chain := middleware.Chain(middlewares...)
	middlewares[0] = func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = "mutated"
			next.ServeHTTP(w, r)
		})
	}
	handler := chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if called != "original" {
		t.Fatalf("middleware = %q, want constructor snapshot", called)
	}
}

func TestLoggerHandlesNilDependencies(t *testing.T) {
	t.Parallel()
	var clk *clockwork.FakeClock
	handler := middleware.Logger(nil, clk)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
}

func TestRecoverHandlesNilLogger(t *testing.T) {
	t.Parallel()
	handler := middleware.Recover(nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
}

func TestOTelHandlesTypedNilTracerProvider(t *testing.T) {
	t.Parallel()
	var provider *typedNilTracerProvider
	handler := middleware.OTel("test", provider)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
}

func TestRequestIDGeneratesAndEchoes(t *testing.T) {
	t.Parallel()
	var captured string
	h := middleware.RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = middleware.RequestIDFromContext(r.Context())
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	if captured == "" {
		t.Fatal("no request ID in context")
	}
	if rr.Header().Get(middleware.RequestIDHeader) != captured {
		t.Errorf("response header %q, context %q",
			rr.Header().Get(middleware.RequestIDHeader), captured)
	}
}

func TestRequestIDReplacesUntrustedInbound(t *testing.T) {
	t.Parallel()
	var captured string
	h := middleware.RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = middleware.RequestIDFromContext(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(middleware.RequestIDHeader, "abc-123")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if captured == "" || captured == "abc-123" {
		t.Errorf("captured = %q, want generated ID", captured)
	}
}

func TestRequestIDFromTrustedPeerRetainsValidatedInbound(t *testing.T) {
	t.Parallel()
	var captured string
	h := middleware.RequestIDFromTrustedPeers(middleware.RequestIDOptions{
		TrustedCIDRs: []string{"10.0.0.0/8"},
	})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = middleware.RequestIDFromContext(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.1.2.3:1234"
	req.Header.Set(middleware.RequestIDHeader, "edge:abc-123")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if captured != "edge:abc-123" {
		t.Fatalf("captured = %q, want trusted inbound ID", captured)
	}
}

func TestRequestIDFromTrustedPeerRejectsInvalidInbound(t *testing.T) {
	t.Parallel()
	var captured string
	h := middleware.RequestIDFromTrustedPeers(middleware.RequestIDOptions{
		TrustedCIDRs: []string{"10.0.0.0/8"},
	})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = middleware.RequestIDFromContext(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.1.2.3:1234"
	invalid := strings.Repeat("x", 129)
	req.Header.Set(middleware.RequestIDHeader, invalid)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if captured == "" || captured == invalid || len(captured) > 128 {
		t.Fatalf("captured = %q, want generated bounded ID", captured)
	}
}

func TestRecoverReturns500(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	h := middleware.Recover(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/panic?step=1", nil))

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := map[string]any{
		"type":     "https://golusoris.dev/errors/internal",
		"title":    "Internal Server Error",
		"status":   float64(http.StatusInternalServerError),
		"detail":   "internal server error",
		"instance": "/panic?step=1",
		"code":     "internal",
		"message":  "internal server error",
	}
	for key, value := range want {
		if body[key] != value {
			t.Errorf("%s = %#v, want %#v", key, body[key], value)
		}
	}
	if !strings.Contains(buf.String(), "panic") {
		t.Errorf("log missing panic: %q", buf.String())
	}
}

func TestCanonicalRecoverLoggerStackLogsPanics(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	handler := middleware.Chain(
		middleware.Recover(logger),
		middleware.Logger(logger, clock.NewFake()),
	)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/panic", nil))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	logs := buf.String()
	if count := strings.Count(logs, `"msg":"http"`); count != 1 {
		t.Fatalf("access log count = %d, want 1; logs = %q", count, logs)
	}
	if !strings.Contains(logs, `"status":500`) {
		t.Fatalf("panic access log missing status 500: %q", logs)
	}
}

func TestLoggerPropagatesPanicIdentity(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	panicValue := &struct{ message string }{message: "keep identity"}
	handler := middleware.Logger(logger, clock.NewFake())(http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) { panic(panicValue) },
	))

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/panic", nil))
	}()

	if recovered != panicValue {
		t.Fatalf("recovered = %#v, want original panic %#v", recovered, panicValue)
	}
	if !strings.Contains(logs.String(), `"status":500`) {
		t.Fatalf("panic access log missing status 500: %q", logs.String())
	}
}

func TestRecoverDoesNotAppendAfterCommittedResponse(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	h := middleware.Recover(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "partial")
		panic("after commit")
	}))
	recorder := httptest.NewRecorder()
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/panic", nil))
	}()
	recoveredErr, ok := recovered.(error)
	if !ok || !errors.Is(recoveredErr, http.ErrAbortHandler) {
		t.Fatalf("recovered = %v, want http.ErrAbortHandler", recovered)
	}
	if recorder.Code != http.StatusOK || recorder.Body.String() != "partial" {
		t.Fatalf("response = %d %q, want 200 partial", recorder.Code, recorder.Body.String())
	}
}

func TestRecoverPropagatesAbortHandler(t *testing.T) {
	t.Parallel()
	h := middleware.Recover(slog.New(slog.DiscardHandler))(http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) },
	))
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/abort", nil))
	}()
	recoveredErr, ok := recovered.(error)
	if !ok || !errors.Is(recoveredErr, http.ErrAbortHandler) {
		t.Fatalf("recovered = %v, want http.ErrAbortHandler", recovered)
	}
}

func TestLoggerEmitsAccessLog(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	h := middleware.Logger(logger, clock.NewFake())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("nope"))
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))

	out := buf.String()
	for _, want := range []string{`"status":418`, `"method":"GET"`, `"path":"/x"`} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q: %q", want, out)
		}
	}
}

func TestLoggerPreservesStreamingFlush(t *testing.T) {
	t.Parallel()
	h := middleware.Logger(slog.New(slog.DiscardHandler), clock.NewFake())(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Error("logger stripped http.Flusher")
				return
			}
			_, _ = io.WriteString(w, "event: ready\n\n")
			flusher.Flush()
		},
	))
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/events", nil))
	if !recorder.Flushed {
		t.Fatal("underlying response was not flushed")
	}
}

func TestSecureHeaders(t *testing.T) {
	t.Parallel()
	h := middleware.SecureHeaders(middleware.SecureHeadersDefaults())(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	if rr.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("X-Frame-Options = %q", rr.Header().Get("X-Frame-Options"))
	}
	if rr.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q", rr.Header().Get("X-Content-Type-Options"))
	}
}

func TestTrustProxyHonorsCIDRs(t *testing.T) {
	t.Parallel()
	var captured string
	var capturedHeaders http.Header
	h := middleware.TrustProxy(middleware.TrustProxyOptions{
		TrustedCIDRs: []string{"10.0.0.0/8"},
	})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = r.RemoteAddr
		capturedHeaders = r.Header.Clone()
	}))

	// Trusted peer -> rewrite.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.1.2.3:54321"
	req.Header.Set("X-Forwarded-For", "203.0.113.5, 10.0.0.1")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if captured != "203.0.113.5" {
		t.Errorf("trusted peer: captured = %q, want 203.0.113.5", captured)
	}

	// Untrusted peer -> preserve.
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "198.51.100.1:54321"
	req.Header.Set("X-Forwarded-For", "203.0.113.5")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if captured != "198.51.100.1:54321" {
		t.Errorf("untrusted peer: captured = %q, want 198.51.100.1:54321", captured)
	}
	for _, name := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-IP"} {
		if got := capturedHeaders.Values(name); len(got) != 0 {
			t.Errorf("untrusted peer retained %s: %q", name, got)
		}
	}
}

func TestTrustProxyProjectsValidatedForwardedProto(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		remoteAddr string
		values     []string
		want       string
	}{
		{name: "trusted https", remoteAddr: "10.1.2.3:1234", values: []string{"https"}, want: "https"},
		{name: "trusted canonicalization", remoteAddr: "10.1.2.3:1234", values: []string{" HTTPS "}, want: "https"},
		{name: "trusted http", remoteAddr: "10.1.2.3:1234", values: []string{"http"}, want: "http"},
		{name: "untrusted", remoteAddr: "192.0.2.3:1234", values: []string{"https"}},
		{name: "duplicate", remoteAddr: "10.1.2.3:1234", values: []string{"https", "http"}},
		{name: "comma chain", remoteAddr: "10.1.2.3:1234", values: []string{"https,http"}},
		{name: "unsupported", remoteAddr: "10.1.2.3:1234", values: []string{"wss"}},
		{name: "missing", remoteAddr: "10.1.2.3:1234"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := ""
			handler := middleware.TrustProxy(middleware.TrustProxyOptions{
				TrustedCIDRs: []string{"10.0.0.0/8"},
			})(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
				got = middleware.ForwardedProtoFromContext(request.Context())
			}))
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.RemoteAddr = test.remoteAddr
			request.Header["X-Forwarded-Proto"] = test.values
			handler.ServeHTTP(httptest.NewRecorder(), request)
			if got != test.want {
				t.Fatalf("ForwardedProtoFromContext() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestTrustProxyCombinesDuplicateForwardedForFields(t *testing.T) {
	t.Parallel()
	var captured string
	h := middleware.TrustProxy(middleware.TrustProxyOptions{
		TrustedCIDRs: []string{"10.0.0.0/8"},
	})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = r.RemoteAddr
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.3:54321"
	req.Header["X-Forwarded-For"] = []string{"198.51.100.99", "203.0.113.5, 10.0.0.2"}
	h.ServeHTTP(httptest.NewRecorder(), req)
	if captured != "203.0.113.5" {
		t.Fatalf("captured = %q, want first untrusted hop 203.0.113.5", captured)
	}
}

func TestETagReturns304(t *testing.T) {
	t.Parallel()
	body := "hello world"
	h := middleware.ETag(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body)
	}))

	// First request: get the ETag.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	etag := rr.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on first response")
	}

	strongEquivalent := strings.TrimPrefix(etag, "W/")
	for _, condition := range []string{etag, strongEquivalent, `"other", ` + etag, "*"} {
		rr2 := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("If-None-Match", condition)
		h.ServeHTTP(rr2, req)
		if rr2.Code != http.StatusNotModified {
			t.Errorf("condition %q: status = %d, want 304", condition, rr2.Code)
		}
	}
}

func TestETagPreservesHandlerValidator(t *testing.T) {
	t.Parallel()
	const validator = `"resource-v7"`
	handler := middleware.ETag(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", validator)
		_, _ = w.Write([]byte("representation"))
	}))

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := first.Header().Get("ETag"); got != validator {
		t.Fatalf("ETag = %q, want handler validator %q", got, validator)
	}

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("If-None-Match", validator)
	conditional := httptest.NewRecorder()
	handler.ServeHTTP(conditional, request)
	if conditional.Code != http.StatusNotModified || conditional.Body.Len() != 0 {
		t.Fatalf("conditional response = %d body %q", conditional.Code, conditional.Body.String())
	}
}

func TestETagBypassesLargeResponses(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("x", 1025)
	h := middleware.ETagWithLimit(1024)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body)
	}))
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/large", nil))
	if recorder.Header().Get("ETag") != "" {
		t.Fatal("large response received an ETag")
	}
	if recorder.Body.String() != body {
		t.Fatalf("body length = %d, want %d", recorder.Body.Len(), len(body))
	}
}

func TestETagPreservesStreamingFlush(t *testing.T) {
	t.Parallel()
	h := middleware.ETag(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("ETag stripped http.Flusher")
			return
		}
		_, _ = io.WriteString(w, "first")
		flusher.Flush()
		_, _ = io.WriteString(w, "second")
	}))
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/stream", nil))
	if !recorder.Flushed {
		t.Fatal("underlying response was not flushed")
	}
	if recorder.Header().Get("ETag") != "" || recorder.Body.String() != "firstsecond" {
		t.Fatalf("stream response = etag %q body %q", recorder.Header().Get("ETag"), recorder.Body.String())
	}
}

func TestOTel_wrapsHandler(t *testing.T) {
	t.Parallel()
	// nil TracerProvider falls back to the global no-op provider — no real OTel
	// setup required for this smoke test.
	mw := middleware.OTel("test.op", nil)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusTeapot {
		t.Errorf("status = %d, want 418", rr.Code)
	}
}

func TestCompressBuilds(t *testing.T) {
	t.Parallel()
	// Compress() only fails on programmer error; the smoke test is that the
	// resulting middleware wraps a handler and serves it transparently when
	// the client doesn't request compression.
	mw, err := middleware.Compress()
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "plain")
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Body.String() != "plain" {
		t.Errorf("body = %q", rr.Body.String())
	}
}
