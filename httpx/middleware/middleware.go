// Package middleware collects golusoris's opinionated HTTP middleware:
// request-ID injection, panic recovery, structured access logs, OpenTelemetry
// instrumentation, secure-header defaults, proxy-trust, compression, and
// ETag generation.
//
// Compose via [Stack] or pick individual middlewares. Order matters:
//
//	router.Use(
//	    middleware.RequestID,
//	    middleware.TrustProxy(trusted),
//	    middleware.Recover(logger),
//	    middleware.Logger(logger),
//	    middleware.OTel(tracer),
//	    middleware.SecureHeaders(middleware.SecureHeadersDefaults()),
//	    middleware.Compress,
//	    middleware.ETag,
//	)
package middleware

import "net/http"

// Middleware is the canonical net/http middleware signature.
type Middleware func(http.Handler) http.Handler

// Chain applies middlewares in order so the first argument is the outermost
// wrapper. Empty chains are a no-op.
func Chain(ms ...Middleware) Middleware {
	return func(next http.Handler) http.Handler {
		for i := len(ms) - 1; i >= 0; i-- {
			next = ms[i](next)
		}
		return next
	}
}
