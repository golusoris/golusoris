// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package middleware

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/golusoris/golusoris/core/validate"
)

// OTel wraps next with the upstream otelhttp middleware using the named
// operation for span naming. Pass an explicit TracerProvider; nil falls back
// to the OTel global (which is a no-op unless an app registers a real one).
//
// Installed with chi's Use, it also records the matched route pattern as
// http.route on the request span and metrics. chi sets Request.Pattern only
// on the request it routes, so otelhttp loses the route as soon as an inner
// middleware passes on r.WithContext(…) (auth, tenancy); the shared chi route
// context survives that.
func OTel(operation string, tp trace.TracerProvider) Middleware {
	if validate.IsNil(tp) {
		tp = otel.GetTracerProvider()
	}
	return func(next http.Handler) http.Handler {
		return otelhttp.NewHandler(routeLabel(next), operation, otelhttp.WithTracerProvider(tp))
	}
}

// routeLabel adds chi's route pattern once routing finished; outside a chi
// router (no route context) or for unmatched paths it adds nothing.
func routeLabel(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		rctx := chi.RouteContext(r.Context())
		if rctx == nil {
			return
		}
		pattern := rctx.RoutePattern()
		if pattern == "" {
			return
		}
		route := semconv.HTTPRoute(pattern)
		trace.SpanFromContext(r.Context()).SetAttributes(route)
		if labeler, ok := otelhttp.LabelerFromContext(r.Context()); ok {
			labeler.Add(route)
		}
	})
}
