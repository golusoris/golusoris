// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package static_test

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/golusoris/golusoris/httpx/static"
)

type typedNilFS struct{}

func (*typedNilFS) Open(string) (fs.File, error) { panic("typed-nil filesystem used") }

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

func TestHandlerTypedNilFilesystemReturnsNotFound(t *testing.T) {
	t.Parallel()
	var filesystem *typedNilFS
	handler := static.Handler(filesystem, static.Options{})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/asset.js", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
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

func TestETagConditionUsesHTTPMethodSemantics(t *testing.T) {
	t.Parallel()
	handler := static.Handler(testFS(), static.Options{})

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/robots.txt", nil))
	etag := first.Header().Get("ETag")
	for _, test := range []struct {
		name   string
		method string
		value  string
		status int
	}{
		{name: "weak list match", method: http.MethodGet, value: `"other", ` + etag, status: http.StatusNotModified},
		{name: "wildcard", method: http.MethodGet, value: "*", status: http.StatusNotModified},
		{name: "unsafe method", method: http.MethodPost, value: etag, status: http.StatusPreconditionFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(test.method, "/robots.txt", nil)
			request.Header.Set("If-None-Match", test.value)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d", recorder.Code, test.status)
			}
		})
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
