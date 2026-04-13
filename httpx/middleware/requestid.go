package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

// RequestIDHeader is the header read + written by [RequestID].
const RequestIDHeader = "X-Request-ID"

type requestIDKey struct{}

// RequestID ensures every request carries an X-Request-ID. If the inbound
// header is set it's trusted verbatim (trust-proxy gates upstream); otherwise
// a fresh UUIDv7 is generated. The ID is stashed on the context + echoed on
// the response.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if id == "" {
			v, err := uuid.NewV7()
			if err == nil {
				id = v.String()
			}
		}
		if id != "" {
			ctx := context.WithValue(r.Context(), requestIDKey{}, id)
			w.Header().Set(RequestIDHeader, id)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequestIDFromContext returns the request ID set by [RequestID] or "".
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey{}).(string)
	return v
}
