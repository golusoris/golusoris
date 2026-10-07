// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package certmagic wires caddyserver/certmagic as an autotls provider.
//
// Config keys (env: APP_HTTP_AUTOTLS_CERTMAGIC_*):
//
//	http.autotls.certmagic.domains  # comma-separated hostnames
//	http.autotls.certmagic.email    # Let's Encrypt contact email
//	http.autotls.certmagic.staging  # use ACME staging (default false)
//	http.autotls.certmagic.timeout  # certificate startup timeout (default 5m)
package certmagic

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	cm "github.com/caddyserver/certmagic"
	"go.uber.org/fx"
	"golang.org/x/net/idna"

	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/core/validate"
)

const (
	// DefaultStartTimeout bounds initial certificate loading and issuance.
	DefaultStartTimeout = 5 * time.Minute
	// MaxStartTimeout prevents effectively unbounded startup configuration.
	MaxStartTimeout = 30 * time.Minute
	// MaxDomains bounds startup work and certificate-cache input.
	MaxDomains = 100
	// MaxDomainBytes bounds one certificate subject before CertMagic normalizes it.
	MaxDomainBytes = 253
)

// Options configures certmagic.
type Options struct {
	Domains []string      `koanf:"domains"`
	Email   string        `koanf:"email"`
	Staging bool          `koanf:"staging"`
	Timeout time.Duration `koanf:"timeout"`
	Storage cm.Storage    `koanf:"-"`
}

// DefaultOptions returns finite startup defaults.
func DefaultOptions() Options {
	return Options{Timeout: DefaultStartTimeout}
}

type managerRuntime struct {
	tlsConfig     *tls.Config
	manageSync    func(context.Context, []string) error
	stopCache     func()
	config        *cm.Config
	configForCert cm.ConfigGetter
}

// Manager owns one CertMagic config and its certificate-cache lifecycle.
type Manager struct {
	domains []string
	timeout time.Duration
	runtime managerRuntime

	mu        sync.Mutex
	started   bool
	closed    bool
	cancel    context.CancelFunc
	startDone chan struct{}
	closeOnce sync.Once
}

// New builds a manager without performing network I/O. Call Start before
// serving its TLS config and Close when the manager is no longer needed.
func New(opts Options) (*Manager, error) {
	normalized, err := normalizeOptions(opts)
	if err != nil {
		return nil, err
	}
	return newManager(normalized, buildRuntime(normalized)), nil
}

func newManager(opts Options, runtime managerRuntime) *Manager {
	return &Manager{
		domains: append([]string(nil), opts.Domains...),
		timeout: opts.Timeout,
		runtime: runtime,
	}
}

// Start synchronously loads or obtains certificates within the configured timeout.
// A Manager accepts one Start call.
func (m *Manager) Start(ctx context.Context) error {
	if ctx == nil {
		return errors.New("httpx/autotls/certmagic: Start context required")
	}
	runCtx, finish, err := m.beginStart(ctx)
	if err != nil {
		return err
	}
	defer finish()
	if err = m.runtime.manageSync(runCtx, m.domains); err != nil {
		return fmt.Errorf("httpx/autotls/certmagic: start: %w", err)
	}
	return nil
}

func (m *Manager) beginStart(ctx context.Context) (context.Context, func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, nil, errors.New("httpx/autotls/certmagic: manager closed")
	}
	if m.started {
		return nil, nil, errors.New("httpx/autotls/certmagic: manager already started")
	}
	m.started = true
	runCtx, cancel := context.WithTimeout(ctx, m.timeout)
	m.cancel = cancel
	m.startDone = make(chan struct{})
	return runCtx, func() { m.finishStart(cancel) }, nil
}

func (m *Manager) finishStart(cancel context.CancelFunc) {
	cancel()
	m.mu.Lock()
	m.cancel = nil
	close(m.startDone)
	m.mu.Unlock()
}

// TLSConfig returns an isolated TLS config backed by this manager.
func (m *Manager) TLSConfig() *tls.Config {
	return m.runtime.tlsConfig.Clone()
}

// Close cancels an in-flight Start and stops the certificate cache.
func (m *Manager) Close() error {
	m.mu.Lock()
	if !m.closed {
		m.closed = true
		if m.cancel != nil {
			m.cancel()
		}
	}
	done := m.startDone
	m.mu.Unlock()
	if done != nil {
		<-done
	}
	m.closeOnce.Do(m.runtime.stopCache)
	return nil
}

func normalizeOptions(opts Options) (Options, error) {
	if err := validateDomainCount(opts.Domains); err != nil {
		return Options{}, err
	}
	domains, err := normalizeDomains(opts.Domains)
	if err != nil {
		return Options{}, err
	}
	timeout, err := normalizeTimeout(opts.Timeout)
	if err != nil {
		return Options{}, err
	}
	storage, err := normalizeStorage(opts.Storage)
	if err != nil {
		return Options{}, err
	}
	opts.Domains = domains
	opts.Timeout = timeout
	opts.Storage = storage
	return opts, nil
}

func validateDomainCount(domains []string) error {
	if len(domains) == 0 {
		return errors.New("httpx/autotls/certmagic: Domains required")
	}
	if len(domains) > MaxDomains {
		return fmt.Errorf("httpx/autotls/certmagic: Domains supports at most %d entries", MaxDomains)
	}
	return nil
}

func normalizeTimeout(timeout time.Duration) (time.Duration, error) {
	if timeout < 0 {
		return 0, errors.New("httpx/autotls/certmagic: Timeout must not be negative")
	}
	if timeout == 0 {
		return DefaultStartTimeout, nil
	}
	if timeout > MaxStartTimeout {
		return 0, fmt.Errorf("httpx/autotls/certmagic: Timeout exceeds %s", MaxStartTimeout)
	}
	return timeout, nil
}

func normalizeStorage(storage cm.Storage) (cm.Storage, error) {
	if storage != nil && validate.IsNil(storage) {
		return nil, errors.New("httpx/autotls/certmagic: Storage must not be typed nil")
	}
	if storage == nil {
		storage = cm.Default.Storage
	}
	if validate.IsNil(storage) {
		return nil, errors.New("httpx/autotls/certmagic: Storage required")
	}
	return storage, nil
}

func normalizeDomains(input []string) ([]string, error) {
	domains := make([]string, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for i, raw := range input {
		if len(strings.TrimSpace(raw)) > MaxDomainBytes {
			return nil, fmt.Errorf("httpx/autotls/certmagic: Domains[%d] exceeds %d bytes", i, MaxDomainBytes)
		}
		domain, err := normalizeDomain(raw)
		if err != nil {
			return nil, fmt.Errorf("httpx/autotls/certmagic: Domains[%d]: %w", i, err)
		}
		if len(domain) > MaxDomainBytes {
			return nil, fmt.Errorf("httpx/autotls/certmagic: Domains[%d] exceeds %d bytes", i, MaxDomainBytes)
		}
		if !cm.SubjectQualifiesForCert(domain) {
			return nil, fmt.Errorf("httpx/autotls/certmagic: Domains[%d] is not a valid certificate subject", i)
		}
		if _, duplicate := seen[domain]; duplicate {
			continue
		}
		seen[domain] = struct{}{}
		domains = append(domains, domain)
	}
	return domains, nil
}

func normalizeDomain(raw string) (string, error) {
	domain := strings.ToLower(strings.TrimSpace(raw))
	wildcard := strings.HasPrefix(domain, "*.")
	if wildcard {
		domain = strings.TrimPrefix(domain, "*.")
	}
	ascii, err := idna.Registration.ToASCII(domain)
	if err != nil {
		return "", fmt.Errorf("invalid domain: %w", err)
	}
	ascii = strings.ToLower(ascii)
	if wildcard {
		ascii = "*." + ascii
	}
	return ascii, nil
}

func buildRuntime(opts Options) managerRuntime {
	var localConfig atomic.Pointer[cm.Config]
	configForCert := func(cm.Certificate) (*cm.Config, error) {
		cfg := localConfig.Load()
		if cfg == nil {
			return nil, errors.New("httpx/autotls/certmagic: local config unavailable")
		}
		return cfg, nil
	}
	cache := cm.NewCache(cm.CacheOptions{GetConfigForCert: configForCert})
	cfg := cm.New(cache, cm.Config{
		RenewalWindowRatio: cm.DefaultRenewalWindowRatio,
		Storage:            opts.Storage,
		KeySource:          cm.StandardKeyGenerator{KeyType: cm.P256},
	})
	cfg.Issuers = []cm.Issuer{newIssuer(cfg, opts)}
	localConfig.Store(cfg)
	tlsConfig := cfg.TLSConfig()
	tlsConfig.MinVersion = tls.VersionTLS12
	tlsConfig.NextProtos = append([]string{"h2", "http/1.1"}, tlsConfig.NextProtos...)
	return managerRuntime{
		tlsConfig:     tlsConfig,
		manageSync:    cfg.ManageSync,
		stopCache:     cache.Stop,
		config:        cfg,
		configForCert: configForCert,
	}
}

func newIssuer(cfg *cm.Config, opts Options) *cm.ACMEIssuer {
	ca := cm.LetsEncryptProductionCA
	if opts.Staging {
		ca = cm.LetsEncryptStagingCA
	}
	email := opts.Email
	if email == "" {
		// CertMagic treats empty as inherit-global; blank normalizes to no contact.
		email = " "
	}
	issuer := cm.NewACMEIssuer(cfg, cm.ACMEIssuer{
		CA:                   ca,
		TestCA:               cm.LetsEncryptStagingCA,
		Email:                email,
		Agreed:               true,
		DisableHTTPChallenge: true,
		CertObtainTimeout:    opts.Timeout,
	})
	issuer.CA = ca
	issuer.TestCA = cm.LetsEncryptStagingCA
	issuer.Email = email
	issuer.Agreed = true
	issuer.AccountKeyPEM = ""
	issuer.ExternalAccount = nil
	issuer.NotBefore = 0
	issuer.NotAfter = 0
	issuer.DisableHTTPChallenge = true
	issuer.DisableTLSALPNChallenge = false
	issuer.ListenHost = ""
	issuer.AltHTTPPort = 0
	issuer.AltTLSALPNPort = 0
	issuer.DNS01Solver = nil
	issuer.TrustedRoots = nil
	issuer.CertObtainTimeout = opts.Timeout
	issuer.Resolver = ""
	issuer.NewAccountFunc = nil
	issuer.Logger = cfg.Logger
	issuer.HTTPProxy = http.ProxyFromEnvironment
	return issuer
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := DefaultOptions()
	if err := cfg.Unmarshal("http.autotls.certmagic", &opts); err != nil {
		return Options{}, fmt.Errorf("httpx/autotls/certmagic: load options: %w", err)
	}
	return opts, nil
}

type moduleParams struct {
	fx.In

	Lifecycle fx.Lifecycle
	Options   Options
	Storage   cm.Storage `optional:"true"`
}

func provideManager(params moduleParams) (*Manager, error) {
	if params.Storage != nil {
		params.Options.Storage = params.Storage
	}
	manager, err := New(params.Options)
	if err != nil {
		return nil, err
	}
	registerLifecycle(params.Lifecycle, manager)
	return manager, nil
}

func registerLifecycle(lifecycle fx.Lifecycle, manager *Manager) {
	lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if err := manager.Start(ctx); err != nil {
				return errors.Join(err, manager.Close())
			}
			return nil
		},
		OnStop: func(context.Context) error { return manager.Close() },
	})
}

func provideTLSConfig(manager *Manager) *tls.Config {
	return manager.TLSConfig()
}

// Module provides a lifecycle-owned Manager and its TLS config.
var Module = fx.Module(
	"golusoris.httpx.autotls.certmagic",
	fx.Provide(loadOptions, provideManager, provideTLSConfig),
)
