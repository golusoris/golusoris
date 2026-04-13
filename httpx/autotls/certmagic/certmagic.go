// Package certmagic wires caddyserver/certmagic as an autotls provider.
//
// Richer than autocert: on-demand issuance, distributed storage, DNS-01 via
// many providers. Trade-off: heavier dep graph.
//
// Config keys (env: APP_HTTP_AUTOTLS_CERTMAGIC_*):
//
//	http.autotls.certmagic.domains  # comma-separated hostnames
//	http.autotls.certmagic.email    # Let's Encrypt contact email
//	http.autotls.certmagic.staging  # use ACME staging (default false)
package certmagic

import (
	"crypto/tls"
	"fmt"

	cm "github.com/caddyserver/certmagic"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
)

// Options configures certmagic.
type Options struct {
	Domains []string `koanf:"domains"`
	Email   string   `koanf:"email"`
	Staging bool     `koanf:"staging"`
}

// New returns a *tls.Config backed by certmagic. An empty Domains list is a
// config error.
func New(opts Options) (*tls.Config, error) {
	if len(opts.Domains) == 0 {
		return nil, fmt.Errorf("httpx/autotls/certmagic: Domains required")
	}
	if opts.Email != "" {
		cm.DefaultACME.Email = opts.Email
	}
	cm.DefaultACME.Agreed = true
	if opts.Staging {
		cm.DefaultACME.CA = cm.LetsEncryptStagingCA
	}
	tlsCfg, err := cm.TLS(opts.Domains)
	if err != nil {
		return nil, fmt.Errorf("httpx/autotls/certmagic: init: %w", err)
	}
	return tlsCfg, nil
}

func loadOptions(cfg *config.Config) (Options, error) {
	var opts Options
	if err := cfg.Unmarshal("http.autotls.certmagic", &opts); err != nil {
		return Options{}, fmt.Errorf("httpx/autotls/certmagic: load options: %w", err)
	}
	return opts, nil
}

// Module provides a *tls.Config via certmagic.
var Module = fx.Module("golusoris.httpx.autotls.certmagic",
	fx.Provide(loadOptions, New),
)
