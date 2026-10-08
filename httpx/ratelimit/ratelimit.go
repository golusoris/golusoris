// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package ratelimit wraps ulule/limiter/v3 as a golusoris middleware.
//
// Defaults to an in-memory store keyed by client IP. Distributed apps can
// supply a shared [limiter.Store] through [Options.Store], directly or by
// decorating Options in the fx graph.
//
// Config keys (env: APP_HTTP_RATELIMIT_*):
//
//	http.ratelimit.rate      # e.g. "100-M" (100/minute), "5-S" (5/second)
//
// Rate format: https://github.com/ulule/limiter?tab=readme-ov-file#usage
package ratelimit

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/ulule/limiter/v3"
	"github.com/ulule/limiter/v3/drivers/store/memory"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/core/validate"
	"github.com/golusoris/golusoris/httpx/middleware"
)

// Options configures the rate-limit middleware.
type Options struct {
	// Rate is the limit per window, e.g. "100-M" for 100/minute, "5-S" for
	// 5/second. See ulule/limiter docs for full grammar.
	Rate string `koanf:"rate"`
	// TrustXFF is retained for config compatibility but rejected as unsafe.
	// Use httpx/middleware.TrustProxy before this middleware.
	//
	// Deprecated: forwarded-header trust must be CIDR-gated centrally.
	TrustXFF bool `koanf:"trust_xff"`
	// Store overrides the process-local in-memory store. Multi-replica
	// deployments must provide a shared implementation.
	Store limiter.Store `koanf:"-"`
}

// DefaultOptions returns no limit (Rate=""). The middleware is a no-op
// until Rate is set.
func DefaultOptions() Options { return Options{} }

// New returns a [middleware.Middleware] enforcing opts. Empty Rate returns
// a pass-through middleware.
func New(opts Options) (middleware.Middleware, error) {
	if opts.TrustXFF {
		return nil, errors.New("httpx/ratelimit: trust_xff is unsafe; use CIDR-gated middleware.TrustProxy")
	}
	if opts.Store != nil && validate.IsNil(opts.Store) {
		return nil, errors.New("httpx/ratelimit: store is typed nil")
	}
	if opts.Rate == "" {
		return identity, nil
	}
	rate, err := limiter.NewRateFromFormatted(opts.Rate)
	if err != nil {
		return nil, fmt.Errorf("httpx/ratelimit: parse rate %q: %w", opts.Rate, err)
	}
	store := opts.Store
	if store == nil {
		store = memory.NewStore()
	}
	lim := limiter.New(store, rate)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, err := lim.Get(r.Context(), limiter.GetIP(r).String())
			if err != nil {
				http.Error(w, "rate limit error", http.StatusInternalServerError)
				return
			}
			w.Header().Set("X-RateLimit-Limit", strconv.FormatInt(ctx.Limit, 10))
			w.Header().Set("X-RateLimit-Remaining", strconv.FormatInt(ctx.Remaining, 10))
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(ctx.Reset, 10))
			if ctx.Reached {
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

func identity(next http.Handler) http.Handler { return next }

func loadOptions(cfg *config.Config) (Options, error) {
	opts := DefaultOptions()
	if err := cfg.Unmarshal("http.ratelimit", &opts); err != nil {
		return Options{}, fmt.Errorf("httpx/ratelimit: load options: %w", err)
	}
	return opts, nil
}

// Module provides a rate-limit [middleware.Middleware].
var Module = fx.Module(
	"golusoris.httpx.ratelimit",
	fx.Provide(loadOptions, New),
)
