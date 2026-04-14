package rangeserve_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/httpx/rangeserve"
)

type memOpener struct {
	data    []byte
	modTime time.Time
	err     error
}

type nopReadSeekCloser struct{ *bytes.Reader }

func (nopReadSeekCloser) Close() error { return nil }

func (m *memOpener) Open(_ context.Context, _ string) (io.ReadSeekCloser, time.Time, error) {
	if m.err != nil {
		return nil, time.Time{}, m.err
	}
	return nopReadSeekCloser{bytes.NewReader(m.data)}, m.modTime, nil
}

func keyFromPath(r *http.Request) string { return r.URL.Path }

func TestHandler_servesFullBody(t *testing.T) {
	t.Parallel()
	body := []byte("hello world")
	h := rangeserve.Handler(&memOpener{data: body, modTime: time.Unix(1000, 0)}, keyFromPath)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/x.txt", nil))
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "hello world", rr.Body.String())
}

// TestHandler_servesRange: RFC 7233 single-range request returns 206 with
// only the requested bytes.
func TestHandler_servesRange(t *testing.T) {
	t.Parallel()
	body := []byte("0123456789")
	h := rangeserve.Handler(&memOpener{data: body, modTime: time.Unix(1000, 0)}, keyFromPath)
	req := httptest.NewRequest(http.MethodGet, "/x.txt", nil)
	req.Header.Set("Range", "bytes=2-5")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	require.Equal(t, http.StatusPartialContent, rr.Code)
	require.Equal(t, "2345", rr.Body.String())
}

func TestHandler_404OnNotExist(t *testing.T) {
	t.Parallel()
	h := rangeserve.Handler(&memOpener{err: os.ErrNotExist}, keyFromPath)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/missing", nil))
	require.Equal(t, http.StatusNotFound, rr.Code)
}

func TestHandler_500OnOtherError(t *testing.T) {
	t.Parallel()
	h := rangeserve.Handler(&memOpener{err: errors.New("boom")}, keyFromPath)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/any", nil))
	require.Equal(t, http.StatusInternalServerError, rr.Code)
	require.Contains(t, rr.Body.String(), "boom")
}

func TestServeReader(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	rangeserve.ServeReader(rr, httptest.NewRequest(http.MethodGet, "/", nil),
		"f.txt", time.Unix(42, 0), strings.NewReader("abc"))
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "abc", rr.Body.String())
}

func TestServeFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	require.NoError(t, os.WriteFile(path, []byte("disk-served"), 0o600))
	rr := httptest.NewRecorder()
	rangeserve.ServeFile(rr, httptest.NewRequest(http.MethodGet, "/", nil), path)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "disk-served", rr.Body.String())
}
