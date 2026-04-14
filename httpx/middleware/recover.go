package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// Recover traps panics, writes a 500, and logs the panic value + stack at
// Error level with the request's X-Request-ID for correlation.
func Recover(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() { //nolint:contextcheck // r.Context() is captured from the request closure
				if rec := recover(); rec != nil {
					logger.ErrorContext(r.Context(), "httpx: panic recovered",
						slog.Any("panic", rec),
						slog.String("method", r.Method),
						slog.String("path", r.URL.Path),
						slog.String("request_id", RequestIDFromContext(r.Context())),
						slog.String("stack", string(debug.Stack())),
					)
					// If headers haven't been written yet, write 500. If they
					// have, stdlib net/http will close the connection.
					w.WriteHeader(http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
