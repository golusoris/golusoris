// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package oidc_test

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/auth/oidc"
	"github.com/golusoris/golusoris/auth/session"
	"github.com/golusoris/golusoris/core/clock"
)

const flowCookie = "sid"

type flowHarness struct {
	flow     *oidc.SessionFlow
	sessions *session.Manager
	store    *session.MemoryStore
	clk      *clockwork.FakeClock
	tokens   []oidc.TokenSet
	errs     []error
	cookie   *http.Cookie
	authURL  *url.URL
}

func newFlowHarness(t *testing.T, maxAge time.Duration) *flowHarness {
	t.Helper()
	mock := startMockOIDCServer(t)
	h := &flowHarness{store: session.NewMemoryStore(), clk: clock.NewFake()}
	var err error
	h.sessions, err = session.NewManager(h.store, session.Options{CookieName: flowCookie})
	require.NoError(t, err)
	h.flow, err = oidc.NewSessionFlow(newTestProvider(t, mock.server.URL), oidc.SessionFlowOptions{
		Sessions: h.sessions,
		Clock:    h.clk,
		MaxAge:   maxAge,
		OnSuccess: func(w http.ResponseWriter, _ *http.Request, _ *session.Session, toks oidc.TokenSet) {
			h.tokens = append(h.tokens, toks)
			w.WriteHeader(http.StatusNoContent)
		},
		OnError: func(w http.ResponseWriter, _ *http.Request, err error) {
			h.errs = append(h.errs, err)
			w.WriteHeader(http.StatusForbidden)
		},
	})
	require.NoError(t, err)
	return h
}

func (h *flowHarness) login(t *testing.T) {
	t.Helper()
	rr := httptest.NewRecorder()
	h.flow.Login(rr, httptest.NewRequest(http.MethodGet, "/auth/oidc/login", nil))
	require.Equal(t, http.StatusFound, rr.Code)
	var err error
	h.authURL, err = url.Parse(rr.Header().Get("Location"))
	require.NoError(t, err)
	h.cookie = sessionCookie(t, rr)
}

func (h *flowHarness) callback(t *testing.T, query url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/auth/oidc/callback?"+query.Encode(), nil)
	if h.cookie != nil {
		req.AddCookie(h.cookie)
	}
	rr := httptest.NewRecorder()
	h.flow.Callback(rr, req)
	return rr
}

// validQuery mimics the IdP redirect; the mock IdP signs "nonce:<n>" codes with nonce n.
func (h *flowHarness) validQuery() url.Values {
	q := h.authURL.Query()
	return url.Values{"state": {q.Get("state")}, "code": {"nonce:" + q.Get("nonce")}}
}

// sessionCookie returns the last cookie set; Callback saves the old ID before Renew sets the new one.
func sessionCookie(t *testing.T, rr *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	var last *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == flowCookie {
			last = c
		}
	}
	require.NotNil(t, last, "no session cookie set")
	return last
}

func TestSessionFlow_LoginCallbackSuccess(t *testing.T) {
	t.Parallel()
	h := newFlowHarness(t, 0)
	h.login(t)

	q := h.authURL.Query()
	require.NotEmpty(t, q.Get("state"))
	require.NotEmpty(t, q.Get("nonce"))
	require.Equal(t, "S256", q.Get("code_challenge_method"))
	require.NotEmpty(t, q.Get("code_challenge"))

	rr := h.callback(t, h.validQuery())
	require.Equal(t, http.StatusNoContent, rr.Code, "errors: %v", h.errs)
	require.Len(t, h.tokens, 1)
	require.Equal(t, "user-42", h.tokens[0].Subject)

	renewed := sessionCookie(t, rr)
	require.NotEqual(t, h.cookie.Value, renewed.Value, "session ID must change after login")
	_, err := h.store.Load(t.Context(), h.cookie.Value)
	require.Error(t, err, "pre-login session must be deleted")
	data, err := h.store.Load(t.Context(), renewed.Value)
	require.NoError(t, err)
	for _, key := range []string{"oidc.state", "oidc.nonce", "oidc.verifier", "oidc.issued_at"} {
		require.NotContains(t, data, key)
	}
}

func TestSessionFlow_CallbackRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(h *flowHarness, q url.Values) url.Values
		want   error
	}{
		{"state mismatch", func(_ *flowHarness, q url.Values) url.Values { q.Set("state", "forged"); return q }, oidc.ErrFlowState},
		{"state missing", func(_ *flowHarness, q url.Values) url.Values { q.Del("state"); return q }, oidc.ErrFlowState},
		{"no session cookie", func(h *flowHarness, q url.Values) url.Values { h.cookie = nil; return q }, oidc.ErrFlowState},
		{"idp error", func(_ *flowHarness, q url.Values) url.Values { q.Set("error", "access_denied"); return q }, oidc.ErrFlowIDP},
		{"code missing", func(_ *flowHarness, q url.Values) url.Values { q.Del("code"); return q }, oidc.ErrFlowCode},
		{"nonce mismatch", func(_ *flowHarness, q url.Values) url.Values { q.Set("code", "nonce:wrong"); return q }, oidc.ErrNonceMismatch},
		{"exchange refused", func(_ *flowHarness, q url.Values) url.Values { q.Set("code", "err-400"); return q }, oidc.ErrExchange},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newFlowHarness(t, 0)
			h.login(t)
			rr := h.callback(t, tc.mutate(h, h.validQuery()))
			require.Equal(t, http.StatusForbidden, rr.Code)
			require.Empty(t, h.tokens)
			require.Len(t, h.errs, 1)
			require.ErrorIs(t, h.errs[0], tc.want)
		})
	}
}

func TestSessionFlow_StateIsSingleUse(t *testing.T) {
	t.Parallel()
	h := newFlowHarness(t, 0)
	h.login(t)
	valid := h.validQuery()

	forged := h.validQuery()
	forged.Set("state", "forged")
	h.callback(t, forged)
	rr := h.callback(t, valid)

	require.Equal(t, http.StatusForbidden, rr.Code)
	require.Len(t, h.errs, 2)
	require.ErrorIs(t, h.errs[1], oidc.ErrFlowState, "a failed callback must consume the stored state")
	require.Empty(t, h.tokens)
}

func TestSessionFlow_MaxAgeBoundary(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		maxAge  time.Duration
		advance time.Duration
		expired bool
	}{
		{"exactly max age", time.Minute, time.Minute, false},
		{"one second past", time.Minute, time.Minute + time.Second, true},
		{"default ten minutes", 0, 10 * time.Minute, false},
		{"past default", 0, 10*time.Minute + time.Second, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newFlowHarness(t, tc.maxAge)
			h.login(t)
			h.clk.Advance(tc.advance)
			h.callback(t, h.validQuery())
			if tc.expired {
				require.Len(t, h.errs, 1)
				require.ErrorIs(t, h.errs[0], oidc.ErrFlowExpired)
				return
			}
			require.Empty(t, h.errs)
			require.Len(t, h.tokens, 1)
		})
	}
}

func TestSessionFlow_IdPErrorTruncated(t *testing.T) {
	t.Parallel()
	h := newFlowHarness(t, 0)
	h.login(t)
	q := h.validQuery()
	q.Set("error", strings.Repeat("x", 65))
	h.callback(t, q)
	require.Len(t, h.errs, 1)
	require.ErrorIs(t, h.errs[0], oidc.ErrFlowIDP)
	require.Contains(t, h.errs[0].Error(), `"`+strings.Repeat("x", 64)+`"`)
}

func TestNewSessionFlow_RejectsInvalidOptions(t *testing.T) {
	t.Parallel()
	mock := startMockOIDCServer(t)
	provider := newTestProvider(t, mock.server.URL)
	sessions, err := session.NewManager(session.NewMemoryStore(), session.Options{})
	require.NoError(t, err)
	valid := oidc.SessionFlowOptions{
		Sessions:  sessions,
		Clock:     clock.NewFake(),
		OnSuccess: func(http.ResponseWriter, *http.Request, *session.Session, oidc.TokenSet) {},
		OnError:   func(http.ResponseWriter, *http.Request, error) {},
	}
	_, err = oidc.NewSessionFlow(provider, valid)
	require.NoError(t, err)

	cases := map[string]func(o *oidc.SessionFlowOptions){
		"nil sessions":     func(o *oidc.SessionFlowOptions) { o.Sessions = nil },
		"nil clock":        func(o *oidc.SessionFlowOptions) { o.Clock = nil },
		"nil OnSuccess":    func(o *oidc.SessionFlowOptions) { o.OnSuccess = nil },
		"nil OnError":      func(o *oidc.SessionFlowOptions) { o.OnError = nil },
		"negative max age": func(o *oidc.SessionFlowOptions) { o.MaxAge = -time.Second },
	}
	for name, mutate := range cases {
		opts := valid
		mutate(&opts)
		_, optErr := oidc.NewSessionFlow(provider, opts)
		require.Error(t, optErr, name)
	}
	_, err = oidc.NewSessionFlow(nil, valid)
	require.Error(t, err, "nil provider")
}

func TestSessionFlow_LoginDiscoveryFailure(t *testing.T) {
	t.Parallel()
	mock := startMockOIDCServer(t)
	provider, err := oidc.NewProvider(t.Context(), oidc.Options{
		IssuerURL:     mock.server.URL + "/missing",
		ClientID:      "test-client",
		RedirectURL:   "https://app.example.com/callback",
		LazyDiscovery: true,
	}, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	sessions, err := session.NewManager(session.NewMemoryStore(), session.Options{})
	require.NoError(t, err)
	var got error
	flow, err := oidc.NewSessionFlow(provider, oidc.SessionFlowOptions{
		Sessions:  sessions,
		Clock:     clock.NewFake(),
		OnSuccess: func(http.ResponseWriter, *http.Request, *session.Session, oidc.TokenSet) {},
		OnError:   func(_ http.ResponseWriter, _ *http.Request, e error) { got = e },
	})
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	flow.Login(rr, httptest.NewRequest(http.MethodGet, "/auth/oidc/login", nil))
	require.Error(t, got)
	require.Empty(t, rr.Header().Get("Location"))
	require.False(t, errors.Is(got, oidc.ErrFlowState))
}
