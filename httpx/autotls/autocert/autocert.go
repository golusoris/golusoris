// Package autocert wires x/crypto/acme/autocert as an autotls provider.
//
// Config keys (env: APP_HTTP_AUTOTLS_AUTOCERT_*):
//
//	http.autotls.autocert.domains  # comma-separated hostnames (e.g. api.example.com,www.example.com)
//	http.autotls.autocert.cache    # directory for cert cache (default "./certs")
//	http.autotls.autocert.email    # contact email sent to Let's Encrypt
//
// The resulting *tls.Config handles ALPN-01 challenges on the same port +
// certificate rotation transparently.
package autocert

import (
	"crypto/tls"
	"errors"
	"fmt"

	"go.uber.org/fx"
	stdacme "golang.org/x/crypto/acme/autocert"

	"github.com/golusoris/golusoris/config"
)

// Options configures autocert.
type Options struct {
	Domains []string `koanf:"domains"`
	Cache   string   `koanf:"cache"`
	Email   string   `koanf:"email"`
}

// DefaultOptions returns sensible defaults (cache="./certs"). Domains must
// be supplied explicitly.
func DefaultOptions() Options {
	return Options{Cache: "./certs"}
}

// New returns a *tls.Config backed by autocert. An empty Domains list is a
// config error — autocert requires HostPolicy.
func New(opts Options) (*tls.Config, error) {
	if len(opts.Domains) == 0 {
		return nil, errors.New("httpx/autotls/autocert: Domains required")
	}
	cache := opts.Cache
	if cache == "" {
		cache = "./certs"
	}
	m := &stdacme.Manager{
		Prompt:     stdacme.AcceptTOS,
		HostPolicy: stdacme.HostWhitelist(opts.Domains...),
		Cache:      stdacme.DirCache(cache),
		Email:      opts.Email,
	}
	return m.TLSConfig(), nil
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := DefaultOptions()
	if err := cfg.Unmarshal("http.autotls.autocert", &opts); err != nil {
		return Options{}, fmt.Errorf("httpx/autotls/autocert: load options: %w", err)
	}
	return opts, nil
}

// Module provides a *tls.Config via autocert. httpx/server picks it up
// automatically (optional dependency; plaintext if absent).
var Module = fx.Module("golusoris.httpx.autotls.autocert",
	fx.Provide(loadOptions, New),
)
