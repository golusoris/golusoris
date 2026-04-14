package tracking_test

import (
	"context"
	"io"
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
	svc := tracking.New(store, []byte("k"))
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
	svc := tracking.New(store, []byte("k"))
	req := httptest.NewRequest(http.MethodGet, "http://example.com/t/open?m=x&r=a&sig=bad", nil)
	rec := httptest.NewRecorder()
	svc.PixelHandler().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, store.list()) // record skipped on bad sig
}

func TestClickHandler_redirects(t *testing.T) {
	t.Parallel()
	store := &memStore{}
	svc := tracking.New(store, []byte("k"))
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
	svc := tracking.New(store, []byte("k"))
	urlStr := svc.ClickURL("http://x/c", "m", "r", "https://ex.com/")

	req := httptest.NewRequest(http.MethodGet, urlStr, nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.5, 10.0.0.1")
	rec := httptest.NewRecorder()
	svc.ClickHandler().ServeHTTP(rec, req)

	require.Equal(t, "203.0.113.5", store.list()[0].IP)
}

func TestClickHandler_rejectsBadSig(t *testing.T) {
	t.Parallel()
	svc := tracking.New(&memStore{}, []byte("k"))
	req := httptest.NewRequest(http.MethodGet, "http://x/c?m=m&r=r&u=https%3A%2F%2Fex.com&sig=bad", nil)
	rec := httptest.NewRecorder()
	svc.ClickHandler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestClickHandler_rejectsMissingParams(t *testing.T) {
	t.Parallel()
	svc := tracking.New(&memStore{}, []byte("k"))
	req := httptest.NewRequest(http.MethodGet, "http://x/c?m=m", nil)
	rec := httptest.NewRecorder()
	svc.ClickHandler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestClickHandler_rejectsNonHTTPTarget(t *testing.T) {
	t.Parallel()
	store := &memStore{}
	svc := tracking.New(store, []byte("k"))
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
	svc := tracking.New(&memStore{}, []byte("k"))
	got := svc.PixelURL("http://x/p", "m1", "a@b")
	require.True(t, strings.HasPrefix(got, "http://x/p?"))
	require.Contains(t, got, "m=m1")
	require.Contains(t, got, "sig=")
}
