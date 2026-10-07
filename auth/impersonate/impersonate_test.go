// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package impersonate_test

import (
	"context"
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

func TestMiddleware_NilDownstreamFailsClosed(t *testing.T) {
	t.Parallel()

	mw, err := impersonate.Middleware(impersonate.Options{
		SessionGet: func(*http.Request) (string, string, bool) { return "user", "", true },
		SessionSet: func(http.ResponseWriter, *http.Request, string, string) error { return nil },
	})
	require.NoError(t, err)
	var typedNil http.HandlerFunc
	for _, next := range []http.Handler{nil, typedNil} {
		response := httptest.NewRecorder()
		require.NotPanics(t, func() {
			mw(next).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
		})
		require.Equal(t, http.StatusInternalServerError, response.Code)
	}
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

func TestMiddleware_ExitQueryDoesNotRestoreElevatedIdentity(t *testing.T) {
	t.Parallel()

	sess := fakeSession{current: "target", original: "admin", ok: true}
	setCalled := false
	mw, err := impersonate.Middleware(impersonate.Options{
		SessionGet: func(_ *http.Request) (string, string, bool) {
			return sess.current, sess.original, sess.ok
		},
		SessionSet: func(_ http.ResponseWriter, _ *http.Request, c, o string) error {
			setCalled = true
			return nil
		},
	})
	require.NoError(t, err)
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		p := impersonate.FromContext(r.Context())
		require.Equal(t, "target", p.Current)
		require.Equal(t, "admin", p.Original)
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/?exit_impersonation=1", nil)
	h.ServeHTTP(w, r)

	require.False(t, setCalled)
	require.Equal(t, "target", sess.current)
	require.Equal(t, "admin", sess.original)
}

func TestExitHandler_POSTTerminatesAfterRestoringActor(t *testing.T) {
	t.Parallel()

	sess := fakeSession{current: "target", original: "admin", ok: true}
	exitCalled := false
	opts := impersonate.Options{
		SessionGet: func(_ *http.Request) (string, string, bool) {
			return sess.current, sess.original, sess.ok
		},
		SessionSet: func(_ http.ResponseWriter, _ *http.Request, current, original string) error {
			sess.current, sess.original = current, original
			return nil
		},
		OnExit: func(_ context.Context, actor, target string) error {
			require.Equal(t, "admin", actor)
			require.Equal(t, "target", target)
			exitCalled = true
			return nil
		},
	}
	exitHandler, err := impersonate.ExitHandler(opts)
	require.NoError(t, err)
	middleware, err := impersonate.Middleware(opts)
	require.NoError(t, err)
	h := middleware(exitHandler)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/exit-impersonation", nil)
	h.ServeHTTP(w, r)

	require.Equal(t, http.StatusNoContent, w.Code)
	require.True(t, exitCalled)
	require.Equal(t, "admin", sess.current)
	require.Empty(t, sess.original)
	require.Empty(t, w.Header().Get(impersonate.HeaderImpersonating))
}

func TestExitHandler_RejectsSafeMethodsWithoutMutation(t *testing.T) {
	t.Parallel()

	setCalled := false
	h, err := impersonate.ExitHandler(impersonate.Options{
		SessionGet: func(_ *http.Request) (string, string, bool) { return "target", "admin", true },
		SessionSet: func(_ http.ResponseWriter, _ *http.Request, _, _ string) error {
			setCalled = true
			return nil
		},
		OnExit: func(context.Context, string, string) error { return nil },
	})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/exit-impersonation", nil)
	h.ServeHTTP(w, r)

	require.Equal(t, http.StatusMethodNotAllowed, w.Code)
	require.Equal(t, http.MethodPost, w.Header().Get("Allow"))
	require.False(t, setCalled)
}

func TestExitHandler_SessionSetFailureIsTerminal(t *testing.T) {
	t.Parallel()

	auditCalled := false
	h, err := impersonate.ExitHandler(impersonate.Options{
		SessionGet: func(_ *http.Request) (string, string, bool) { return "target", "admin", true },
		SessionSet: func(_ http.ResponseWriter, _ *http.Request, _, _ string) error {
			return errors.New("store unavailable")
		},
		OnExit: func(context.Context, string, string) error {
			auditCalled = true
			return nil
		},
	})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/exit-impersonation", nil))

	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.True(t, auditCalled)
}

func TestBegin_RejectsNesting(t *testing.T) {
	t.Parallel()

	opts := impersonate.Options{
		SessionGet:    func(_ *http.Request) (string, string, bool) { return "user", "admin", true },
		SessionSet:    func(_ http.ResponseWriter, _ *http.Request, _, _ string) error { return nil },
		OnImpersonate: func(context.Context, string, string) error { return nil },
	}
	err := impersonate.Begin(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil), opts, "victim")
	require.Error(t, err)
}

func TestBeginAuditFailurePreventsSessionMutation(t *testing.T) {
	t.Parallel()
	setCalled := false
	errAudit := errors.New("audit unavailable")
	opts := impersonate.Options{
		SessionGet: func(_ *http.Request) (string, string, bool) { return "admin", "", true },
		SessionSet: func(_ http.ResponseWriter, _ *http.Request, _, _ string) error {
			setCalled = true
			return nil
		},
		OnImpersonate: func(context.Context, string, string) error { return errAudit },
	}
	err := impersonate.Begin(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/", nil),
		opts,
		"target",
	)
	require.ErrorIs(t, err, errAudit)
	require.False(t, setCalled)
}

func TestBeginRejectsEmptyOrSameTarget(t *testing.T) {
	t.Parallel()
	opts := impersonate.Options{
		SessionGet:    func(_ *http.Request) (string, string, bool) { return "admin", "", true },
		SessionSet:    func(_ http.ResponseWriter, _ *http.Request, _, _ string) error { return nil },
		OnImpersonate: func(context.Context, string, string) error { return nil },
	}
	for _, target := range []string{"", " ", "admin"} {
		err := impersonate.Begin(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodPost, "/", nil),
			opts,
			target,
		)
		require.Error(t, err)
	}
}

func TestExitAuditFailurePreventsSessionMutation(t *testing.T) {
	t.Parallel()
	setCalled := false
	handler, err := impersonate.ExitHandler(impersonate.Options{
		SessionGet: func(_ *http.Request) (string, string, bool) { return "target", "admin", true },
		SessionSet: func(_ http.ResponseWriter, _ *http.Request, _, _ string) error {
			setCalled = true
			return nil
		},
		OnExit: func(context.Context, string, string) error { return errors.New("audit unavailable") },
	})
	require.NoError(t, err)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", nil))
	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.False(t, setCalled)
}
