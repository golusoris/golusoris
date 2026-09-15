// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package impersonate lets an admin act as another user with full audit
// trail. The impersonator's original principal is stored in a session
// claim so the action can be reverted.
//
// Wire-up:
//
//	mw, err := impersonate.Middleware(impersonate.Options{
//	    SessionGet:  func(r *http.Request) (string, string, bool) { ... },
//	    SessionSet:  func(w, r, current, original string) { ... },
//	    OnImpersonate: func(actor, target string) { auditLog.Record(...) },
//	})
//
// In the principal extractor, prefer the `current` user when set,
// falling back to the original. A banner header tells the UI to show
// "You are impersonating X — exit".
package impersonate

import (
	"context"
	"errors"
	"net/http"

	gerr "github.com/golusoris/golusoris/core/errors"
)

// HeaderImpersonating is set by the middleware on every response so
// the frontend can render a banner.
const HeaderImpersonating = "X-Impersonating"

// QueryParamExit triggers the revert flow when present on a request.
const QueryParamExit = "exit_impersonation"

type ctxKey struct{}

// Principal records the active and original user IDs on the request
// context. Original is empty when the user is not impersonating.
type Principal struct {
	Current  string
	Original string
}

// FromContext returns the Principal stored on ctx (zero value if absent).
func FromContext(ctx context.Context) Principal {
	p, _ := ctx.Value(ctxKey{}).(Principal)
	return p
}

// WithContext puts p on ctx.
func WithContext(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// Options wires the middleware to the app's session store and audit log.
type Options struct {
	// SessionGet returns (current, original, ok). original is "" when not impersonating.
	SessionGet func(r *http.Request) (current, original string, ok bool)
	// SessionSet persists the new (current, original) pair.
	SessionSet func(w http.ResponseWriter, r *http.Request, current, original string) error
	// OnImpersonate is called when an actor begins impersonating target.
	OnImpersonate func(actorUserID, targetUserID string)
	// OnExit is called when the actor reverts to themselves.
	OnExit func(actorUserID, targetUserID string)
}

// Middleware injects a Principal into every request context based on
// the session and handles the exit flow. Returns an error when
// SessionGet or SessionSet is nil.
func Middleware(opts Options) (func(http.Handler) http.Handler, error) {
	if opts.SessionGet == nil || opts.SessionSet == nil {
		return nil, errors.New("impersonate: SessionGet + SessionSet required")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handleImpersonation(w, r, next, opts)
		})
	}, nil
}

// handleImpersonation resolves the session's current/original principal for
// r, applies the exit-impersonation flow when requested, and forwards the
// request to next with the resulting Principal attached to its context. It
// writes the response itself (without calling next) when there is no
// session to attach, or when reverting the session fails.
func handleImpersonation(w http.ResponseWriter, r *http.Request, next http.Handler, opts Options) {
	cur, orig, ok := opts.SessionGet(r)
	if !ok {
		next.ServeHTTP(w, r)
		return
	}

	if exitRequested(r, orig) {
		var reverted bool
		cur, orig, reverted = revertImpersonation(w, r, opts, cur, orig)
		if !reverted {
			return
		}
	}

	if orig != "" {
		w.Header().Set(HeaderImpersonating, cur)
	}
	ctx := WithContext(r.Context(), Principal{Current: cur, Original: orig})
	next.ServeHTTP(w, r.WithContext(ctx))
}

// exitRequested reports whether r asks to revert an active impersonation.
func exitRequested(r *http.Request, orig string) bool {
	return r.URL.Query().Get(QueryParamExit) != "" && orig != ""
}

// revertImpersonation persists the reverted (orig, "") session pair,
// notifies opts.OnExit, and returns the new (current, original) pair. The
// third return value is false when SessionSet failed and a 500 has already
// been written to w — the caller must stop processing the request without
// calling next.
func revertImpersonation(w http.ResponseWriter, r *http.Request, opts Options, cur, orig string) (newCur, newOrig string, ok bool) {
	if err := opts.SessionSet(w, r, orig, ""); err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return cur, orig, false
	}
	if opts.OnExit != nil {
		opts.OnExit(orig, cur)
	}
	return orig, "", true
}

// Begin starts an impersonation: replaces the current principal with
// targetUserID and records the original. Returns gerr.CodeForbidden when
// already impersonating (no nesting).
func Begin(w http.ResponseWriter, r *http.Request, opts Options, targetUserID string) error {
	cur, orig, ok := opts.SessionGet(r)
	if !ok {
		return errors.New("impersonate: no session")
	}
	if orig != "" {
		return gerr.Forbidden("already impersonating")
	}
	if err := opts.SessionSet(w, r, targetUserID, cur); err != nil {
		return err
	}
	if opts.OnImpersonate != nil {
		opts.OnImpersonate(cur, targetUserID)
	}
	return nil
}
