package middleware

import "net/http"

// SecureHeadersOptions configures the headers written by [SecureHeaders].
type SecureHeadersOptions struct {
	// FrameOptions sets X-Frame-Options. Default "DENY".
	FrameOptions string
	// ContentTypeOptions sets X-Content-Type-Options. Default "nosniff".
	ContentTypeOptions string
	// ReferrerPolicy sets Referrer-Policy. Default "strict-origin-when-cross-origin".
	ReferrerPolicy string
	// HSTS sets Strict-Transport-Security. Empty omits the header (appropriate
	// when the app is served over plaintext, e.g. behind a TLS-terminating LB
	// that does not forward TLS info). Default is empty; set to e.g.
	// "max-age=31536000; includeSubDomains" in production.
	HSTS string
	// PermissionsPolicy sets Permissions-Policy. Default empty.
	PermissionsPolicy string
	// ContentSecurityPolicy sets Content-Security-Policy. Default empty.
	ContentSecurityPolicy string
}

// SecureHeadersDefaults returns a conservative baseline appropriate for
// most API + web apps.
func SecureHeadersDefaults() SecureHeadersOptions {
	return SecureHeadersOptions{
		FrameOptions:       "DENY",
		ContentTypeOptions: "nosniff",
		ReferrerPolicy:     "strict-origin-when-cross-origin",
	}
}

// SecureHeaders writes the configured security headers before invoking next.
// Headers with empty string values are skipped.
func SecureHeaders(opts SecureHeadersOptions) Middleware {
	headers := map[string]string{
		"X-Frame-Options":           opts.FrameOptions,
		"X-Content-Type-Options":    opts.ContentTypeOptions,
		"Referrer-Policy":           opts.ReferrerPolicy,
		"Strict-Transport-Security": opts.HSTS,
		"Permissions-Policy":        opts.PermissionsPolicy,
		"Content-Security-Policy":   opts.ContentSecurityPolicy,
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			for k, v := range headers {
				if v != "" {
					h.Set(k, v)
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
