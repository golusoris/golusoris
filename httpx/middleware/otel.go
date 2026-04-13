package middleware

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// OTel wraps next with the upstream otelhttp middleware using the named
// operation for span naming. Pass an explicit TracerProvider; nil falls back
// to the OTel global (which is a no-op unless an app registers a real one).
func OTel(operation string, tp trace.TracerProvider) Middleware {
	if tp == nil {
		tp = otel.GetTracerProvider()
	}
	return func(next http.Handler) http.Handler {
		return otelhttp.NewHandler(next, operation, otelhttp.WithTracerProvider(tp))
	}
}
