// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package tracking_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/notify/tracking"
)

type memStore struct {
	mu     sync.Mutex
	events []tracking.Event
}

func (m *memStore) Record(_ context.Context, ev tracking.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, ev)
	return nil
}

func (m *memStore) list() []tracking.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]tracking.Event, len(m.events))
	copy(out, m.events)
	return out
}

func TestPixelHandler_recordsOpen(t *testing.T) {
	t.Parallel()
	store := &memStore{}
	svc := tracking.New(store, []byte("k"), nil)
	urlStr := svc.PixelURL("http://example.com/t/open", "msg1", "alice@example.com")

	req := httptest.NewRequest(http.MethodGet, urlStr, nil)
	req.Header.Set("User-Agent", "Mail/1.0")
	rec := httptest.NewRecorder()
	svc.PixelHandler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "image/gif", rec.Header().Get("Content-Type"))
	body, _ := io.ReadAll(rec.Body)
	require.Equal(t, []byte{0x47, 0x49, 0x46}, body[:3]) // GIF magic

	events := store.list()
	require.Len(t, events, 1)
	require.Equal(t, tracking.KindOpen, events[0].Kind)
	require.Equal(t, "msg1", events[0].MessageID)
	require.Equal(t, "alice@example.com", events[0].Recipient)
	require.Equal(t, "Mail/1.0", events[0].UserAgent)
}

func TestPixelHandler_servesEvenOnBadSig(t *testing.T) {
	t.Parallel()
	store := &memStore{}
	svc := tracking.New(store, []byte("k"), nil)
	req := httptest.NewRequest(http.MethodGet, "http://example.com/t/open?m=x&r=a&sig=bad", nil)
	rec := httptest.NewRecorder()
	svc.PixelHandler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, store.list()) // record skipped on bad sig
}

func TestClickHandler_redirects(t *testing.T) {
	t.Parallel()
	store := &memStore{}
	svc := tracking.New(store, []byte("k"), nil)
	target := "https://example.com/landing?x=1"
	urlStr := svc.ClickURL("http://example.com/t/click", "msg2", "bob@example.com", target)

	req := httptest.NewRequest(http.MethodGet, urlStr, nil)
	req.RemoteAddr = "10.0.0.1:1234"
	rec := httptest.NewRecorder()
	svc.ClickHandler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, target, rec.Header().Get("Location"))

	events := store.list()
	require.Len(t, events, 1)
	require.Equal(t, tracking.KindClick, events[0].Kind)
	require.Equal(t, target, events[0].URL)
	require.Equal(t, "10.0.0.1:1234", events[0].IP)
}

func TestClickHandler_forwardedFor(t *testing.T) {
	t.Parallel()
	store := &memStore{}
	svc := tracking.New(store, []byte("k"), nil)
	urlStr := svc.ClickURL("http://x/c", "m", "r", "https://ex.com/")

	req := httptest.NewRequest(http.MethodGet, urlStr, nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.5, 10.0.0.1")
	rec := httptest.NewRecorder()
	svc.ClickHandler().ServeHTTP(rec, req)

	require.Equal(t, "203.0.113.5", store.list()[0].IP)
}

func TestClickHandler_rejectsBadSig(t *testing.T) {
	t.Parallel()
	svc := tracking.New(&memStore{}, []byte("k"), nil)
	req := httptest.NewRequest(http.MethodGet, "http://x/c?m=m&r=r&u=https%3A%2F%2Fex.com&sig=bad", nil)
	rec := httptest.NewRecorder()
	svc.ClickHandler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestClickHandler_rejectsMissingParams(t *testing.T) {
	t.Parallel()
	svc := tracking.New(&memStore{}, []byte("k"), nil)
	req := httptest.NewRequest(http.MethodGet, "http://x/c?m=m", nil)
	rec := httptest.NewRecorder()
	svc.ClickHandler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestClickHandler_rejectsNonHTTPTarget(t *testing.T) {
	t.Parallel()
	store := &memStore{}
	svc := tracking.New(store, []byte("k"), nil)
	// Signed but with a javascript: target — must be rejected.
	urlStr := svc.ClickURL("http://x/c", "m", "r", "javascript:alert(1)")

	req := httptest.NewRequest(http.MethodGet, urlStr, nil)
	rec := httptest.NewRecorder()
	svc.ClickHandler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Empty(t, store.list())
}

func TestPixelURL_containsExpectedFields(t *testing.T) {
	t.Parallel()
	svc := tracking.New(&memStore{}, []byte("k"), nil)
	got := svc.PixelURL("http://x/p", "m1", "a@b")
	require.True(t, strings.HasPrefix(got, "http://x/p?"))
	require.Contains(t, got, "m=m1")
	require.Contains(t, got, "sig=")
}

// failStore fails every Record call.
type failStore struct{ err error }

func (f failStore) Record(context.Context, tracking.Event) error { return f.err }

func newLoggedService(t *testing.T, store tracking.Store) (*tracking.Service, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	return tracking.New(store, []byte("k"), logger), &buf
}

// Negative path: a store failure must not break the pixel; it is logged.
func TestPixelHandler_storeFailureStillServesAndLogs(t *testing.T) {
	t.Parallel()
	svc, buf := newLoggedService(t, failStore{err: errors.New("db down")})
	req := httptest.NewRequest(http.MethodGet, svc.PixelURL("http://x/p", "msg1", "a@b"), nil)
	rec := httptest.NewRecorder()
	svc.PixelHandler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "image/gif", rec.Header().Get("Content-Type"))
	require.Contains(t, buf.String(), "notify/tracking: record event")
	require.Contains(t, buf.String(), "kind=open")
	require.Contains(t, buf.String(), "db down")
}

// Negative path: a store failure must not break the redirect; it is logged.
func TestClickHandler_storeFailureStillRedirectsAndLogs(t *testing.T) {
	t.Parallel()
	svc, buf := newLoggedService(t, failStore{err: errors.New("db down")})
	target := "https://example.com/landing"
	req := httptest.NewRequest(http.MethodGet, svc.ClickURL("http://x/c", "msg2", "b@c", target), nil)
	rec := httptest.NewRecorder()
	svc.ClickHandler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusFound, rec.Code)
	require.Equal(t, target, rec.Header().Get("Location"))
	require.Contains(t, buf.String(), "kind=click")
	require.Contains(t, buf.String(), "message_id=msg2")
}

// Boundary: a nil logger must fall back to slog.Default() rather than panic.
func TestNew_nilLoggerDoesNotPanic(t *testing.T) {
	t.Parallel()
	svc := tracking.New(failStore{err: errors.New("db down")}, []byte("k"), nil)
	req := httptest.NewRequest(http.MethodGet, svc.PixelURL("http://x/p", "m", "r"), nil)
	rec := httptest.NewRecorder()
	require.NotPanics(t, func() { svc.PixelHandler().ServeHTTP(rec, req) })
	require.Equal(t, http.StatusOK, rec.Code)
}
