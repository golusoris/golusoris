// Package ui mounts the River UI (queue dashboard) at a configurable
// prefix with optional basic-auth. Apps typically mount this behind an
// admin-only sub-router, never on the public router.
//
// Usage:
//
//	fx.Invoke(func(r chi.Router, client *jobs.Client, logger *slog.Logger) error {
//	    h, err := ui.NewHandler(ui.Options{
//	        Client: client, Prefix: "/jobs/ui",
//	        User:   "admin", Password: secret,
//	    }, logger)
//	    if err != nil { return err }
//	    r.Mount("/jobs/ui", h)
//	    return nil
//	})
//
// The handler is stateful — it holds caches + background queries. Call
// [Start] once to initialize; it stops when the supplied ctx is
// canceled. For fx-managed lifecycle use [Module] instead.
package ui

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	riverui "riverqueue.com/riverui"

	"github.com/golusoris/golusoris/jobs"
)

// Options configures the UI handler.
type Options struct {
	// Client is the river client backing the UI. Required.
	Client *jobs.Client
	// Prefix is the URL path prefix the UI is served under (e.g.
	// "/jobs/ui"). Required for the UI's asset paths to resolve.
	Prefix string
	// User + Password enable basic-auth. Both empty = no auth (only
	// safe on localhost / behind a gated admin router).
	User     string
	Password string
	// HideJobArgs hides the Args column by default (args may carry PII).
	HideJobArgs bool
}

// NewHandler builds the UI http.Handler. Callers MUST call handler.Start
// exactly once before serving requests; [Module] handles that.
func NewHandler(opts Options, logger *slog.Logger) (*riverui.Handler, error) {
	if opts.Client == nil {
		return nil, errors.New("jobs/ui: Options.Client is required")
	}
	endpoints := riverui.NewEndpoints[pgx.Tx](opts.Client, nil)
	h, err := riverui.NewHandler(&riverui.HandlerOpts{
		Endpoints:                endpoints,
		Logger:                   logger,
		Prefix:                   opts.Prefix,
		JobListHideArgsByDefault: opts.HideJobArgs,
	})
	if err != nil {
		return nil, fmt.Errorf("jobs/ui: build handler: %w", err)
	}
	return h, nil
}

// WithBasicAuth wraps h with constant-time basic-auth. When both creds
// are empty, returns h unchanged. Useful for apps that don't have a
// dedicated admin router + middleware stack.
func WithBasicAuth(h http.Handler, user, pass string) http.Handler {
	if user == "" && pass == "" {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		userOK := subtle.ConstantTimeCompare([]byte(u), []byte(user)) == 1
		passOK := subtle.ConstantTimeCompare([]byte(p), []byte(pass)) == 1
		if !ok || !userOK || !passOK {
			w.Header().Set("WWW-Authenticate", `Basic realm="river-ui"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// Start initializes the handler's background services. Must be called
// before serving requests. The returned cancel func stops them.
func Start(ctx context.Context, h *riverui.Handler) (context.CancelFunc, error) {
	bgCtx, cancel := context.WithCancel(ctx)
	if err := h.Start(bgCtx); err != nil {
		cancel()
		return nil, fmt.Errorf("jobs/ui: start: %w", err)
	}
	return cancel, nil
}

// Verify the Handler type works via direct river import (compile-time
// sanity check that our river major-version is compatible with riverui).
var _ = river.QueueDefault
