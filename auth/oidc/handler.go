// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package oidc

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/golusoris/golusoris/auth/session"
	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/validate"
)

const (
	defaultFlowMaxAge = 10 * time.Minute
	maxIDPErrorLen    = 64

	flowStateKey    = "oidc.state"
	flowNonceKey    = "oidc.nonce"
	flowVerifierKey = "oidc.verifier"
	flowIssuedKey   = "oidc.issued_at"
)

// Sentinels passed to [SessionFlowOptions.OnError] for refused callbacks.
var (
	// ErrFlowState indicates the callback state was missing or did not match the session.
	ErrFlowState = errors.New("auth/oidc: flow state missing or mismatched")
	// ErrFlowExpired indicates the callback arrived after [SessionFlowOptions.MaxAge].
	ErrFlowExpired = errors.New("auth/oidc: flow expired")
	// ErrFlowIDP indicates the IdP redirected back with an error parameter.
	ErrFlowIDP = errors.New("auth/oidc: identity provider returned an error")
	// ErrFlowCode indicates the callback carried no authorization code.
	ErrFlowCode = errors.New("auth/oidc: callback code missing")
)

// SessionFlowOptions configure a [SessionFlow].
type SessionFlowOptions struct {
	// Sessions stores state, nonce and PKCE verifier between login and callback. Required.
	Sessions *session.Manager
	// Clock bounds the flow age. Required.
	Clock clock.Clock
	// MaxAge bounds the time between login and callback (default 10m).
	MaxAge time.Duration
	// OnSuccess runs after the session moved to a fresh ID with the flow keys
	// removed; it stores the signed-in identity in sess, saves it and responds. Required.
	OnSuccess func(w http.ResponseWriter, r *http.Request, sess *session.Session, toks TokenSet)
	// OnError responds to a failed login or callback; err wraps a flow sentinel,
	// a [Provider.Complete] sentinel, or a session or discovery failure. Required.
	OnError func(w http.ResponseWriter, r *http.Request, err error)
}

type flowProvider interface {
	Begin(ctx context.Context, state string) (AuthRequest, error)
	Complete(ctx context.Context, code string, req AuthRequest) (TokenSet, error)
}

// SessionFlow serves the OIDC login redirect and callback over [Provider.Begin]
// and [Provider.Complete], keeping state, nonce and PKCE verifier in the
// server-side session.
type SessionFlow struct {
	provider flowProvider
	opts     SessionFlowOptions
}

// NewSessionFlow validates opts and returns a flow driving p.
func NewSessionFlow(p *Provider, opts SessionFlowOptions) (*SessionFlow, error) {
	if p == nil {
		return nil, errors.New("auth/oidc: session flow: provider is required")
	}
	return newSessionFlow(p, opts)
}

func newSessionFlow(p flowProvider, opts SessionFlowOptions) (*SessionFlow, error) {
	switch {
	case opts.Sessions == nil:
		return nil, errors.New("auth/oidc: session flow: sessions are required")
	case validate.IsNil(opts.Clock):
		return nil, errors.New("auth/oidc: session flow: clock is required")
	case opts.OnSuccess == nil || opts.OnError == nil:
		return nil, errors.New("auth/oidc: session flow: OnSuccess and OnError are required")
	case opts.MaxAge < 0:
		return nil, errors.New("auth/oidc: session flow: MaxAge must not be negative")
	case opts.MaxAge == 0:
		opts.MaxAge = defaultFlowMaxAge
	}
	return &SessionFlow{provider: p, opts: opts}, nil
}

// Login stores a fresh state, nonce and verifier in the session and redirects
// to the IdP authorization URL.
func (f *SessionFlow) Login(w http.ResponseWriter, r *http.Request) {
	state, err := randomSecret("generate state")
	if err != nil {
		f.opts.OnError(w, r, err)
		return
	}
	req, err := f.provider.Begin(r.Context(), state)
	if err != nil {
		f.opts.OnError(w, r, fmt.Errorf("auth/oidc: login: %w", err))
		return
	}
	sess, err := f.opts.Sessions.Load(r)
	if err != nil {
		f.opts.OnError(w, r, fmt.Errorf("auth/oidc: login: %w", err))
		return
	}
	sess.Set(flowStateKey, state)
	sess.Set(flowNonceKey, req.Nonce)
	sess.Set(flowVerifierKey, req.Verifier)
	sess.Set(flowIssuedKey, f.opts.Clock.Now().Unix())
	if err := f.opts.Sessions.SaveContext(r.Context(), w, sess); err != nil {
		f.opts.OnError(w, r, fmt.Errorf("auth/oidc: login: %w", err))
		return
	}
	http.Redirect(w, r, req.URL, http.StatusFound)
}

type flowState struct {
	state  string
	req    AuthRequest
	issued time.Time
}

// Callback consumes the stored flow state once, validates the IdP response,
// completes the code exchange, renews the session ID and calls OnSuccess.
func (f *SessionFlow) Callback(w http.ResponseWriter, r *http.Request) {
	sess, stored, err := f.consume(w, r)
	if err != nil {
		f.opts.OnError(w, r, err)
		return
	}
	code, err := f.validateCallback(r, stored)
	if err != nil {
		f.opts.OnError(w, r, err)
		return
	}
	toks, err := f.provider.Complete(r.Context(), code, stored.req)
	if err != nil {
		f.opts.OnError(w, r, fmt.Errorf("auth/oidc: callback: %w", err))
		return
	}
	if err := f.opts.Sessions.Renew(r.Context(), w, sess); err != nil {
		f.opts.OnError(w, r, fmt.Errorf("auth/oidc: callback: %w", err))
		return
	}
	f.opts.OnSuccess(w, r, sess, toks)
}

// consume removes the flow keys before validation so a callback replay fails.
func (f *SessionFlow) consume(w http.ResponseWriter, r *http.Request) (*session.Session, flowState, error) {
	sess, err := f.opts.Sessions.Load(r)
	if err != nil {
		return nil, flowState{}, fmt.Errorf("auth/oidc: callback: %w", err)
	}
	state, _ := sess.Get(flowStateKey).(string)
	nonce, _ := sess.Get(flowNonceKey).(string)
	verifier, _ := sess.Get(flowVerifierKey).(string)
	issued := unixTime(sess.Get(flowIssuedKey))
	for _, key := range []string{flowStateKey, flowNonceKey, flowVerifierKey, flowIssuedKey} {
		sess.Delete(key)
	}
	if err := f.opts.Sessions.SaveContext(r.Context(), w, sess); err != nil {
		return nil, flowState{}, fmt.Errorf("auth/oidc: callback: %w", err)
	}
	return sess, flowState{state: state, req: AuthRequest{Verifier: verifier, Nonce: nonce}, issued: issued}, nil
}

func (f *SessionFlow) validateCallback(r *http.Request, stored flowState) (string, error) {
	query := r.URL.Query()
	if idpErr := query.Get("error"); idpErr != "" {
		return "", fmt.Errorf("%w: %s", ErrFlowIDP, strconv.Quote(truncate(idpErr, maxIDPErrorLen)))
	}
	got := query.Get("state")
	if got == "" || stored.state == "" || subtle.ConstantTimeCompare([]byte(got), []byte(stored.state)) != 1 {
		return "", ErrFlowState
	}
	// Truncate: issued is stored at second precision.
	if stored.issued.IsZero() || f.opts.Clock.Now().Truncate(time.Second).Sub(stored.issued) > f.opts.MaxAge {
		return "", ErrFlowExpired
	}
	code := query.Get("code")
	if code == "" || stored.req.Verifier == "" {
		return "", ErrFlowCode
	}
	return code, nil
}

// unixTime accepts the integer kinds a session store may hand back, JSON stores included.
func unixTime(v any) time.Time {
	switch val := v.(type) {
	case int64:
		return time.Unix(val, 0)
	case int:
		return time.Unix(int64(val), 0)
	case float64:
		return time.Unix(int64(val), 0)
	default:
		return time.Time{}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
