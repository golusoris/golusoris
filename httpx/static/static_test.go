package static_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/golusoris/golusoris/httpx/static"
)

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":     {Data: []byte("<h1>home</h1>")},
		"about.html":     {Data: []byte("<h1>about</h1>")},
		"robots.txt":     {Data: []byte("User-agent: *\nAllow: /\n")},
		"sub/index.html": {Data: []byte("<h1>sub</h1>")},
	}
}

func TestServesFile(t *testing.T) {
	t.Parallel()
	h := static.Handler(testFS(), static.Options{})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/about.html", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body, _ := io.ReadAll(rr.Body)
	if !strings.Contains(string(body), "about") {
		t.Errorf("body = %q", body)
	}
}

func TestETagRoundTripReturns304(t *testing.T) {
	t.Parallel()
	h := static.Handler(testFS(), static.Options{})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/robots.txt", nil))
	etag := rr.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on first response")
	}

	rr2 := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/robots.txt", nil)
	req.Header.Set("If-None-Match", etag)
	h.ServeHTTP(rr2, req)
	if rr2.Code != http.StatusNotModified {
		t.Errorf("status = %d, want 304", rr2.Code)
	}
}

func TestIndexFallback(t *testing.T) {
	t.Parallel()
	h := static.Handler(testFS(), static.Options{})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "home") {
		t.Errorf("body = %q", rr.Body.String())
	}
}

func TestNoIndexFallback404sRoot(t *testing.T) {
	t.Parallel()
	h := static.Handler(testFS(), static.Options{NoIndexFallback: true})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestCacheControlApplied(t *testing.T) {
	t.Parallel()
	h := static.Handler(testFS(), static.Options{CacheControl: "no-store"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/robots.txt", nil))
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q", got)
	}
}
