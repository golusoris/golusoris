package middleware_test

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/httpx/middleware"
)

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

func TestRequestIDTrustsInbound(t *testing.T) {
	t.Parallel()
	var captured string
	h := middleware.RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = middleware.RequestIDFromContext(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(middleware.RequestIDHeader, "abc-123")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if captured != "abc-123" {
		t.Errorf("captured = %q, want %q", captured, "abc-123")
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
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d", rr.Code)
	}
	if !strings.Contains(buf.String(), "panic") {
		t.Errorf("log missing panic: %q", buf.String())
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
	h := middleware.TrustProxy(middleware.TrustProxyOptions{
		TrustedCIDRs: []string{"10.0.0.0/8"},
	})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = r.RemoteAddr
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

	// Second request with If-None-Match: expect 304.
	rr2 := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("If-None-Match", etag)
	h.ServeHTTP(rr2, req)
	if rr2.Code != http.StatusNotModified {
		t.Errorf("status = %d, want 304", rr2.Code)
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
