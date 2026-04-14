package impersonate_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/auth/impersonate"
)

type fakeSession struct {
	current  string
	original string
	ok       bool
}

func TestMiddleware_PassesThroughWithoutSession(t *testing.T) {
	t.Parallel()

	called := false
	mw := impersonate.Middleware(impersonate.Options{
		SessionGet: func(_ *http.Request) (string, string, bool) { return "", "", false },
		SessionSet: func(_ http.ResponseWriter, _ *http.Request, _, _ string) error { return nil },
	})
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { called = true }))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(w, r)

	require.True(t, called)
	require.Empty(t, w.Header().Get(impersonate.HeaderImpersonating))
}

func TestMiddleware_AddsHeaderWhenImpersonating(t *testing.T) {
	t.Parallel()

	mw := impersonate.Middleware(impersonate.Options{
		SessionGet: func(_ *http.Request) (string, string, bool) { return "target", "admin", true },
		SessionSet: func(_ http.ResponseWriter, _ *http.Request, _, _ string) error { return nil },
	})
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		p := impersonate.FromContext(r.Context())
		require.Equal(t, "target", p.Current)
		require.Equal(t, "admin", p.Original)
	}))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	require.Equal(t, "target", w.Header().Get(impersonate.HeaderImpersonating))
}

func TestMiddleware_ExitFlow(t *testing.T) {
	t.Parallel()

	sess := fakeSession{current: "target", original: "admin", ok: true}
	exitCalled := false
	mw := impersonate.Middleware(impersonate.Options{
		SessionGet: func(_ *http.Request) (string, string, bool) {
			return sess.current, sess.original, sess.ok
		},
		SessionSet: func(_ http.ResponseWriter, _ *http.Request, c, o string) error {
			sess.current, sess.original = c, o
			return nil
		},
		OnExit: func(_, _ string) { exitCalled = true },
	})
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		p := impersonate.FromContext(r.Context())
		require.Equal(t, "admin", p.Current)
		require.Empty(t, p.Original)
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/?exit_impersonation=1", nil)
	h.ServeHTTP(w, r)

	require.True(t, exitCalled)
	require.Equal(t, "admin", sess.current)
	require.Empty(t, sess.original)
}

func TestBegin_RejectsNesting(t *testing.T) {
	t.Parallel()

	opts := impersonate.Options{
		SessionGet: func(_ *http.Request) (string, string, bool) { return "user", "admin", true },
		SessionSet: func(_ http.ResponseWriter, _ *http.Request, _, _ string) error { return nil },
	}
	err := impersonate.Begin(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil), opts, "victim")
	require.Error(t, err)
}
