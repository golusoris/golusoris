// Package ratelimit wraps ulule/limiter/v3 as a golusoris middleware.
//
// Defaults to an in-memory store keyed by client IP. For distributed apps
// behind multiple replicas, swap in a redis-backed store via
// [fx.Decorate] (cache/redis, Step 8).
//
// Config keys (env: APP_HTTP_RATELIMIT_*):
//
//	http.ratelimit.rate      # e.g. "100-M" (100/minute), "5-S" (5/second)
//	http.ratelimit.trust_xff # trust X-Forwarded-For for peer IP (default false)
//
// Rate format: https://github.com/ulule/limiter?tab=readme-ov-file#usage
package ratelimit

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/ulule/limiter/v3"
	"github.com/ulule/limiter/v3/drivers/store/memory"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/httpx/middleware"
)

// Options configures the rate-limit middleware.
type Options struct {
	// Rate is the limit per window, e.g. "100-M" for 100/minute, "5-S" for
	// 5/second. See ulule/limiter docs for full grammar.
	Rate string `koanf:"rate"`
	// TrustXFF trusts X-Forwarded-For to identify the real client. Only
	// enable behind a trusted reverse proxy (use httpx/middleware.TrustProxy
	// for the canonical implementation).
	TrustXFF bool `koanf:"trust_xff"`
}

// DefaultOptions returns no limit (Rate=""). The middleware is a no-op
// until Rate is set.
func DefaultOptions() Options { return Options{} }

// New returns a [middleware.Middleware] enforcing opts. Empty Rate returns
// a pass-through middleware.
func New(opts Options) (middleware.Middleware, error) {
	if opts.Rate == "" {
		return identity, nil
	}
	rate, err := limiter.NewRateFromFormatted(opts.Rate)
	if err != nil {
		return nil, fmt.Errorf("httpx/ratelimit: parse rate %q: %w", opts.Rate, err)
	}
	store := memory.NewStore()
	lim := limiter.New(store, rate,
		limiter.WithTrustForwardHeader(opts.TrustXFF),
	)
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
var Module = fx.Module("golusoris.httpx.ratelimit",
	fx.Provide(loadOptions, New),
)
