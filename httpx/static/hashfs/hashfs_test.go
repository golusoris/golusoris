// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hashfs_test

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/golusoris/golusoris/httpx/static/hashfs"
)

type typedNilFS struct{}

func (*typedNilFS) Open(string) (fs.File, error) { panic("typed-nil filesystem used") }

func TestHashNameIsStableForContent(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"logo.png": {Data: []byte("pixels")},
	}
	f := hashfs.New(fsys)
	n1 := f.HashName("logo.png")
	n2 := f.HashName("logo.png")
	if n1 != n2 {
		t.Errorf("non-deterministic hash: %q vs %q", n1, n2)
	}
	if n1 == "logo.png" {
		t.Errorf("HashName did not transform: %q", n1)
	}
}

func TestTypedNilFilesystemFailsClosed(t *testing.T) {
	t.Parallel()
	var filesystem *typedNilFS
	wrapped := hashfs.New(filesystem)
	if got := wrapped.HashName("asset.js"); got != "asset.js" {
		t.Fatalf("HashName = %q, want original name", got)
	}
	recorder := httptest.NewRecorder()
	hashfs.Handler(filesystem).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/asset.js", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}

func TestHandlerServesHashedRequest(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"logo.png": {Data: []byte("pixels")},
	}
	f := hashfs.New(fsys)
	hashed := f.HashName("logo.png")

	h := hashfs.Handler(f)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/"+hashed, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	body, _ := io.ReadAll(rr.Body)
	if string(body) != "pixels" {
		t.Errorf("body = %q", body)
	}
	if cc := rr.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age=31536000") {
		t.Errorf("Cache-Control = %q, want year-long max-age", cc)
	}
}
