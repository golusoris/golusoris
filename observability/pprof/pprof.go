// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package pprof mounts stdlib net/http/pprof handlers under /debug/pprof
// with optional basic-auth gating. Never mount on the public router
// unprotected — profile endpoints stream raw runtime data.
//
// Usage:
//
//	r.Mount("/debug/pprof", pprof.Handler(pprof.Options{
//	    User: cfg.String("pprof.user"), Password: cfg.String("pprof.password"),
//	}))
//
// Or wire via fx on a dedicated admin chi.Router (recommended so admin
// traffic bypasses public middleware like CORS/CSRF).
package pprof

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	nethttppprof "net/http/pprof"
	"runtime/pprof"

	"github.com/go-chi/chi/v5"
)

// Options configures the pprof handler.
type Options struct {
	// User + Password enable basic-auth gating. Both empty = no auth (only
	// safe on localhost / internal admin endpoints).
	User     string
	Password string
}

// Handler returns a chi.Router that mounts all stdlib pprof endpoints.
// Complete credentials require basic-auth on every endpoint. A partial
// credential configuration rejects every request.
func Handler(opts Options) http.Handler {
	r := chi.NewRouter()
	if opts.User != "" || opts.Password != "" {
		r.Use(basicAuth(opts.User, opts.Password))
	}
	// Stdlib registers /debug/pprof/* on http.DefaultServeMux — we re-expose
	// the same handlers under a chi router so they compose with our stack.
	r.HandleFunc("/", nethttppprof.Index)
	r.HandleFunc("/cmdline", nethttppprof.Cmdline)
	r.HandleFunc("/profile", nethttppprof.Profile)
	r.HandleFunc("/symbol", nethttppprof.Symbol)
	r.HandleFunc("/trace", nethttppprof.Trace)
	// Expose all runtime profiles (heap, goroutine, threadcreate, block,
	// mutex, allocs — whatever is registered). Using Handler directly so
	// Index's auto-discovery works.
	for _, p := range pprof.Profiles() {
		name := p.Name()
		r.Handle("/"+name, nethttppprof.Handler(name))
	}
	return r
}

func basicAuth(user, pass string) func(http.Handler) http.Handler {
	wantUser := credentialDigest(user)
	wantPass := credentialDigest(pass)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if user == "" || pass == "" {
				w.Header().Set("WWW-Authenticate", `Basic realm="pprof"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			u, p, ok := r.BasicAuth()
			gotUser := credentialDigest(u)
			gotPass := credentialDigest(p)
			userOK := subtle.ConstantTimeCompare(gotUser[:], wantUser[:]) == 1
			passOK := subtle.ConstantTimeCompare(gotPass[:], wantPass[:]) == 1
			if !ok || !userOK || !passOK {
				w.Header().Set("WWW-Authenticate", `Basic realm="pprof"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func credentialDigest(value string) [sha256.Size]byte {
	return sha256.Sum256([]byte(value))
}
