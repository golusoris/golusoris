// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

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

import (
	"net/http"
	"slices"
)

// Middleware is the canonical net/http middleware signature.
type Middleware func(http.Handler) http.Handler

// Chain applies middlewares in order so the first argument is the outermost
// wrapper. Empty chains are a no-op.
func Chain(ms ...Middleware) Middleware {
	ms = slices.Clone(ms)
	return func(next http.Handler) http.Handler {
		for _, m := range slices.Backward(ms) {
			if m != nil {
				next = m(next)
			}
		}
		return next
	}
}
