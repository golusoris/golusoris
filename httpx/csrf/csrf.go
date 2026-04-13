// Package csrf exposes gorilla/csrf as a golusoris middleware. Uses the
// double-submit cookie pattern — the token is set as a cookie + must be
// echoed in an X-CSRF-Token header or a `gorilla.csrf.Token` form field on
// unsafe requests.
//
// Config keys (env: APP_HTTP_CSRF_*):
//
//	http.csrf.secret  # 32-byte key (hex or base64); required to enable
//	http.csrf.secure  # require HTTPS for the cookie (default true)
//	http.csrf.domain  # cookie domain (default "")
//	http.csrf.path    # cookie path (default "/")
package csrf

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"

	gcsrf "github.com/gorilla/csrf"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/httpx/middleware"
)

// Options tunes the CSRF middleware.
type Options struct {
	// Secret is a 32-byte key, hex- or base64-encoded. Required.
	Secret string `koanf:"secret"`
	Secure bool   `koanf:"secure"`
	Domain string `koanf:"domain"`
	Path   string `koanf:"path"`
}

// DefaultOptions returns Secure=true, Path="/". Secret is intentionally
// zero-valued: CSRF is no-op until an app sets it.
func DefaultOptions() Options {
	return Options{Secure: true, Path: "/"}
}

// New returns a CSRF [middleware.Middleware]. When opts.Secret is empty the
// returned middleware is a no-op — apps without sessions don't need CSRF.
func New(opts Options) (middleware.Middleware, error) {
	if opts.Secret == "" {
		return identity, nil
	}
	key, err := decodeKey(opts.Secret)
	if err != nil {
		return nil, fmt.Errorf("httpx/csrf: decode secret: %w", err)
	}
	csrfOpts := []gcsrf.Option{
		gcsrf.Secure(opts.Secure),
	}
	if opts.Domain != "" {
		csrfOpts = append(csrfOpts, gcsrf.Domain(opts.Domain))
	}
	if opts.Path != "" {
		csrfOpts = append(csrfOpts, gcsrf.Path(opts.Path))
	}
	return gcsrf.Protect(key, csrfOpts...), nil
}

// Token extracts the CSRF token for the current request. Embed in forms as
// `<input name="gorilla.csrf.Token" value="{{ .CSRFToken }}">` or return in
// an X-CSRF-Token response header for SPA clients.
func Token(r *http.Request) string { return gcsrf.Token(r) }

func identity(next http.Handler) http.Handler { return next }

func decodeKey(s string) ([]byte, error) {
	if b, err := hex.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := base64.URLEncoding.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	return nil, fmt.Errorf("secret must decode to 32 bytes (hex or base64)")
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := DefaultOptions()
	if err := cfg.Unmarshal("http.csrf", &opts); err != nil {
		return Options{}, fmt.Errorf("httpx/csrf: load options: %w", err)
	}
	return opts, nil
}

// Module provides a CSRF [middleware.Middleware].
var Module = fx.Module("golusoris.httpx.csrf",
	fx.Provide(loadOptions, New),
)
