// Package csrf exposes a CSRF middleware as a golusoris module.
//
// Uses [filippo.io/csrf/gorilla] — a drop-in replacement for
// github.com/gorilla/csrf that enforces same-origin requests via
// Fetch metadata / Origin headers (the approach Go 1.25 adopted in
// net/http.CrossOriginProtection). This avoids GHSA-82ff-hg59-8x73
// (TrustedOrigins scheme confusion) entirely and works with
// reverse-proxies and localhost without per-app configuration.
//
// Token-based fields (X-CSRF-Token / gorilla.csrf.Token form field)
// remain exposed for API compatibility with legacy templates but are
// ignored — same-origin validation is the authoritative check.
//
// Config keys (env: APP_HTTP_CSRF_*):
//
//	http.csrf.secret  # 32-byte key (hex or base64); required to enable
package csrf

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"

	gcsrf "filippo.io/csrf/gorilla"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/httpx/middleware"
)

// Options tunes the CSRF middleware.
//
// Secure/Domain/Path are retained for config compatibility but are
// ignored — filippo.io/csrf/gorilla enforces same-origin via Fetch
// metadata, not cookies.
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
	return gcsrf.Protect(key), nil
}

// Token extracts the CSRF token for the current request. Embed in forms as
// `<input name="gorilla.csrf.Token" value="{{ .CSRFToken }}">` or return in
// an X-CSRF-Token response header for SPA clients.
func Token(r *http.Request) string { return gcsrf.Token(r) } //nolint:staticcheck // retained for template compatibility; token value is not authoritative

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
	return nil, errors.New("secret must decode to 32 bytes (hex or base64)")
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
