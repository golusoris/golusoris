package middleware

import (
	"log/slog"
	"net/http"

	"github.com/golusoris/golusoris/clock"
)

// statusRecorder captures the status code + bytes written so Logger can log
// them post-hoc without asking the handler for cooperation.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err //nolint:wrapcheck // passthrough of ResponseWriter contract
}

// Logger emits one structured access log per request (at Info level for 2xx/
// 3xx, Warn for 4xx, Error for 5xx). Uses [clock.Clock] for time so tests
// can inject a fake.
func Logger(logger *slog.Logger, clk clock.Clock) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := clk.Now()
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			elapsed := clk.Since(start)

			level := slog.LevelInfo
			switch {
			case rec.status >= http.StatusInternalServerError:
				level = slog.LevelError
			case rec.status >= http.StatusBadRequest:
				level = slog.LevelWarn
			}
			logger.LogAttrs(r.Context(), level, "http",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", statusOrDefault(rec.status)),
				slog.Int("bytes", rec.bytes),
				slog.Duration("elapsed", elapsed),
				slog.String("remote", r.RemoteAddr),
				slog.String("request_id", RequestIDFromContext(r.Context())),
			)
		})
	}
}

func statusOrDefault(s int) int {
	if s == 0 {
		return http.StatusOK
	}
	return s
}
