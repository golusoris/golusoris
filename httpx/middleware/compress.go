package middleware

import (
	"net/http"

	"github.com/CAFxX/httpcompression"
)

// Compress enables negotiated response compression (gzip + brotli + zstd when
// supported by the client). Built on CAFxX/httpcompression. Constructor
// returns an error when the underlying compressor wiring fails, which only
// happens on programmer error — apps can ignore it in wiring if they prefer.
func Compress() (Middleware, error) {
	adapter, err := httpcompression.DefaultAdapter()
	if err != nil {
		return nil, err //nolint:wrapcheck // library's own error is already descriptive
	}
	return func(next http.Handler) http.Handler {
		return adapter(next)
	}, nil
}
