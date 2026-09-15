// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package impersonate_test

import (
	"errors"
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

func TestMiddleware_RequiresSessionHooks(t *testing.T) {
	t.Parallel()

	_, err := impersonate.Middleware(impersonate.Options{})
	require.Error(t, err)
}

func TestMiddleware_PassesThroughWithoutSession(t *testing.T) {
	t.Parallel()

	called := false
	mw, err := impersonate.Middleware(impersonate.Options{
		SessionGet: func(_ *http.Request) (string, string, bool) { return "", "", false },
		SessionSet: func(_ http.ResponseWriter, _ *http.Request, _, _ string) error { return nil },
	})
	require.NoError(t, err)
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { called = true }))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	h.ServeHTTP(w, r)

	require.True(t, called)
	require.Empty(t, w.Header().Get(impersonate.HeaderImpersonating))
}

func TestMiddleware_AddsHeaderWhenImpersonating(t *testing.T) {
	t.Parallel()

	mw, err := impersonate.Middleware(impersonate.Options{
		SessionGet: func(_ *http.Request) (string, string, bool) { return "target", "admin", true },
		SessionSet: func(_ http.ResponseWriter, _ *http.Request, _, _ string) error { return nil },
	})
	require.NoError(t, err)
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
	mw, err := impersonate.Middleware(impersonate.Options{
		SessionGet: func(_ *http.Request) (string, string, bool) {
			return sess.current, sess.original, sess.ok
		},
		SessionSet: func(_ http.ResponseWriter, _ *http.Request, c, o string) error {
			sess.current, sess.original = c, o
			return nil
		},
		OnExit: func(_, _ string) { exitCalled = true },
	})
	require.NoError(t, err)
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

func TestMiddleware_ExitFlow_SessionSetFails(t *testing.T) {
	t.Parallel()

	exitCalled := false
	nextCalled := false
	mw, err := impersonate.Middleware(impersonate.Options{
		SessionGet: func(_ *http.Request) (string, string, bool) { return "target", "admin", true },
		SessionSet: func(_ http.ResponseWriter, _ *http.Request, _, _ string) error {
			return errors.New("store unavailable")
		},
		OnExit: func(_, _ string) { exitCalled = true },
	})
	require.NoError(t, err)
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { nextCalled = true }))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/?exit_impersonation=1", nil)
	h.ServeHTTP(w, r)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.False(t, exitCalled, "OnExit must not fire when the session store failed")
	require.False(t, nextCalled, "next must not be called when the revert failed")
}

func TestMiddleware_ExitParamIgnoredWhenNotImpersonating(t *testing.T) {
	t.Parallel()

	mw, err := impersonate.Middleware(impersonate.Options{
		SessionGet: func(_ *http.Request) (string, string, bool) { return "solo", "", true },
		SessionSet: func(_ http.ResponseWriter, _ *http.Request, _, _ string) error {
			t.Fatal("SessionSet must not be called when there is no active impersonation to exit")
			return nil
		},
	})
	require.NoError(t, err)
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		p := impersonate.FromContext(r.Context())
		require.Equal(t, "solo", p.Current)
		require.Empty(t, p.Original)
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/?exit_impersonation=1", nil)
	h.ServeHTTP(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	require.Empty(t, w.Header().Get(impersonate.HeaderImpersonating))
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
