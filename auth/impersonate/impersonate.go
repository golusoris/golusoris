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
//	    OnImpersonate: func(ctx context.Context, actor, target string) error {
//	        return auditLog.Record(ctx, actor, target)
//	    },
//	})
//
// In the principal extractor, prefer the `current` user when set,
// falling back to the original. A banner header tells the UI to show
// "You are impersonating X — exit".
// Mount [ExitHandler] as a POST route behind CSRF middleware.
package impersonate

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	gerr "github.com/golusoris/golusoris/core/errors"
	"github.com/golusoris/golusoris/core/validate"
)

// HeaderImpersonating is set by the middleware on every response so
// the frontend can render a banner.
const HeaderImpersonating = "X-Impersonating"

// QueryParamExit is retained for source compatibility. Query-string exits are
// ignored; use [ExitHandler] on a CSRF-protected route instead.
//
// Deprecated: query-string state changes are unsafe.
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
	// OnImpersonate durably audits an impersonation before session mutation.
	OnImpersonate func(ctx context.Context, actorUserID, targetUserID string) error
	// OnExit durably audits an exit before session mutation.
	OnExit func(ctx context.Context, actorUserID, targetUserID string) error
}

// Middleware injects a Principal into every request context based on the
// session. Returns an error when SessionGet or SessionSet is nil.
func Middleware(opts Options) (func(http.Handler) http.Handler, error) {
	if err := validateOptions(opts); err != nil {
		return nil, err
	}
	return func(next http.Handler) http.Handler {
		if validate.IsNil(next) {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "downstream handler unavailable", http.StatusInternalServerError)
			})
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handleImpersonation(w, r, next, opts)
		})
	}, nil
}

// handleImpersonation resolves the session's current/original principal and
// forwards the request with the Principal attached to its context.
func handleImpersonation(w http.ResponseWriter, r *http.Request, next http.Handler, opts Options) {
	cur, orig, ok := opts.SessionGet(r)
	if !ok {
		next.ServeHTTP(w, r)
		return
	}

	if orig != "" {
		w.Header().Set(HeaderImpersonating, cur)
	}
	ctx := WithContext(r.Context(), Principal{Current: cur, Original: orig})
	next.ServeHTTP(w, r.WithContext(ctx))
}

// ExitHandler returns a terminal, POST-only impersonation exit endpoint.
// Mount it behind the application's CSRF middleware. It never delegates to a
// downstream handler, so the exit request cannot execute with the restored
// actor's privileges.
func ExitHandler(opts Options) (http.Handler, error) {
	if err := validateOptions(opts); err != nil {
		return nil, err
	}
	if opts.OnExit == nil {
		return nil, errors.New("impersonate: OnExit audit hook required")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		exit(w, r, opts)
	}), nil
}

func exit(w http.ResponseWriter, r *http.Request, opts Options) {
	cur, orig, ok := opts.SessionGet(r)
	if !ok {
		http.Error(w, "session required", http.StatusUnauthorized)
		return
	}
	if orig == "" {
		http.Error(w, "not impersonating", http.StatusConflict)
		return
	}
	cur = strings.TrimSpace(cur)
	orig = strings.TrimSpace(orig)
	if cur == "" || orig == "" || cur == orig {
		http.Error(w, "invalid impersonation session", http.StatusConflict)
		return
	}
	if err := opts.OnExit(r.Context(), orig, cur); err != nil {
		http.Error(w, "audit error", http.StatusInternalServerError)
		return
	}
	if err := opts.SessionSet(w, r, orig, ""); err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	w.Header().Del(HeaderImpersonating)
	w.WriteHeader(http.StatusNoContent)
}

func validateOptions(opts Options) error {
	if opts.SessionGet == nil || opts.SessionSet == nil {
		return errors.New("impersonate: SessionGet + SessionSet required")
	}
	return nil
}

// Begin starts an impersonation: replaces the current principal with
// targetUserID and records the original. Returns gerr.CodeForbidden when
// already impersonating (no nesting).
func Begin(w http.ResponseWriter, r *http.Request, opts Options, targetUserID string) error {
	if err := validateOptions(opts); err != nil {
		return err
	}
	if opts.OnImpersonate == nil {
		return errors.New("impersonate: OnImpersonate audit hook required")
	}
	cur, orig, ok := opts.SessionGet(r)
	if !ok {
		return errors.New("impersonate: no session")
	}
	cur = strings.TrimSpace(cur)
	targetUserID = strings.TrimSpace(targetUserID)
	if cur == "" || targetUserID == "" {
		return gerr.Validation("impersonate: actor and target required")
	}
	if cur == targetUserID {
		return gerr.Validation("impersonate: target must differ from actor")
	}
	if orig != "" {
		return gerr.Forbidden("already impersonating")
	}
	if err := opts.OnImpersonate(r.Context(), cur, targetUserID); err != nil {
		return fmt.Errorf("impersonate: audit begin: %w", err)
	}
	if err := opts.SessionSet(w, r, targetUserID, cur); err != nil {
		return fmt.Errorf("impersonate: set session: %w", err)
	}
	return nil
}
