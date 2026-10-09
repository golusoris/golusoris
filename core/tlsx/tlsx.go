// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package tlsx builds crypto/tls configurations from PEM certificate, key, and
// CA files and reloads those files lazily during handshakes. Certificates that
// cert-manager, a Vault agent, or a Kubernetes Secret volume rotate on disk
// reach new connections without a restart and without a background goroutine.
//
//	r, err := tlsx.NewReloader(tlsx.Files{Cert: "tls.crt", Key: "tls.key", CA: "ca.crt"}, tlsx.Options{})
//	srv.TLSConfig = r.ServerConfig(tls.RequireAndVerifyClientCert)
//
// A handshake re-reads the files at most once per [Options.MinInterval]. A
// failed reload keeps the last good material, logs a warning, and is reported
// by [Reloader.LastError] until a later reload succeeds.
//
// Clients that never rotate their files take a one-shot config instead:
//
//	cfg, err := tlsx.LoadClientConfig(tlsx.Files{CA: "ca.crt"})
package tlsx

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/core/clock"
)

// DefaultMinInterval is the least time between two reads of the files.
const DefaultMinInterval = 30 * time.Second

// Sentinel errors returned by [NewReloader], [LoadClientConfig], [ParseClientAuth], and handshakes.
var (
	ErrNoFiles       = errors.New("tlsx: no certificate or CA file configured")
	ErrPartialPair   = errors.New("tlsx: cert and key files must be set together")
	ErrEmptyCA       = errors.New("tlsx: CA file holds no PEM certificate")
	ErrNoCertificate = errors.New("tlsx: server config requires a certificate")
	ErrNoClientCA    = errors.New("tlsx: verifying client certificates requires a CA file")
	ErrClientAuth    = errors.New("tlsx: unknown client auth mode")
)

// Files names the PEM files a [Reloader] or [LoadClientConfig] reads. Cert and
// Key are set together; CA alone suits a client that only verifies its server.
type Files struct {
	Cert string // certificate chain, leaf first
	Key  string // private key matching Cert
	CA   string // CA bundle that verifies the peer
}

// Options tunes a [Reloader]. The zero value is usable.
type Options struct {
	// MinInterval is the least time between two file reads; non-positive
	// values use [DefaultMinInterval].
	MinInterval time.Duration
	// Clock drives MinInterval; nil uses the wall clock.
	Clock clock.Clock
	// Logger receives reload results; nil uses slog.Default().
	Logger *slog.Logger
}

// material is one parsed, immutable generation of the files.
type material struct {
	cert *tls.Certificate // nil without Files.Cert
	pool *x509.CertPool   // nil without Files.CA
	raw  [3][]byte        // cert, key, CA bytes this generation was parsed from
}

// Reloader serves the current certificate and CA pool to TLS handshakes.
// It is safe for concurrent use.
type Reloader struct {
	files       Files
	minInterval time.Duration
	clk         clock.Clock
	logger      *slog.Logger

	mu        sync.Mutex
	cur       *material
	checkedAt time.Time
	lastErr   error
}

// NewReloader loads files once and fails closed when they are missing,
// unparsable, or the key does not match the certificate.
func NewReloader(files Files, opts Options) (*Reloader, error) {
	if files == (Files{}) {
		return nil, ErrNoFiles
	}
	r := &Reloader{files: files, minInterval: opts.MinInterval, clk: opts.Clock, logger: opts.Logger}
	if r.minInterval <= 0 {
		r.minInterval = DefaultMinInterval
	}
	if r.clk == nil {
		r.clk = clockwork.NewRealClock()
	}
	if r.logger == nil {
		r.logger = slog.Default()
	}
	m, err := files.load()
	if err != nil {
		return nil, err
	}
	r.cur = m
	r.checkedAt = r.clk.Now()
	return r, nil
}

// LoadClientConfig reads files once into a TLS 1.3 client config. CA sets
// RootCAs (unset means system roots); Cert and Key add a client certificate.
// The zero Files is valid and yields a system-roots config. Nothing is re-read
// after this call: use [Reloader.ClientConfig] to follow rotated files.
func LoadClientConfig(files Files) (*tls.Config, error) {
	m, err := files.load()
	if err != nil {
		return nil, err
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: m.pool}
	if m.cert != nil {
		cfg.Certificates = []tls.Certificate{*m.cert}
	}
	return cfg, nil
}

// LastError reports the most recent reload failure, or nil once a later
// reload succeeds or finds the files unchanged.
func (r *Reloader) LastError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastErr
}

// ServerConfig returns a TLS 1.3 server config whose certificate and client
// CA pool follow the files. Set extra fields (NextProtos, …) on the result
// before its first handshake: each handshake clones it.
func (r *Reloader) ServerConfig(clientAuth tls.ClientAuthType) *tls.Config {
	base := &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: clientAuth}
	base.GetConfigForClient = func(*tls.ClientHelloInfo) (*tls.Config, error) {
		return r.handshakeConfig(base)
	}
	return base
}

func (r *Reloader) handshakeConfig(base *tls.Config) (*tls.Config, error) {
	m := r.current()
	if m.cert == nil {
		return nil, ErrNoCertificate
	}
	if verifiesClient(base.ClientAuth) && m.pool == nil {
		return nil, ErrNoClientCA
	}
	cfg := base.Clone()
	cfg.GetConfigForClient = nil
	cfg.Certificates = []tls.Certificate{*m.cert}
	cfg.ClientCAs = m.pool
	return cfg, nil
}

// ClientConfig returns a TLS 1.3 client config. The client certificate follows
// the files on every handshake; RootCAs is the CA pool current at this call
// (nil without Files.CA, meaning system roots), so call ClientConfig per
// connection to follow a rotated CA bundle.
func (r *Reloader) ClientConfig(serverName string) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		ServerName: serverName,
		RootCAs:    r.current().pool,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			if cert := r.current().cert; cert != nil {
				return cert, nil
			}
			// An empty certificate tells crypto/tls to send none.
			return &tls.Certificate{}, nil
		},
	}
}

// current returns the live material, re-reading the files once MinInterval
// has passed since the last read.
func (r *Reloader) current() *material {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.clk.Now()
	if now.Sub(r.checkedAt) < r.minInterval {
		return r.cur
	}
	r.checkedAt = now
	changed, err := r.reload()
	r.lastErr = err
	switch {
	case err != nil:
		r.logger.Warn("tlsx: reload failed; keeping last good certificate", slog.String("error", err.Error()))
	case changed:
		r.logger.Info("tlsx: reloaded certificate files", slog.String("cert_file", r.files.Cert), slog.String("ca_file", r.files.CA))
	}
	return r.cur
}

// reload swaps in freshly parsed material when the file bytes changed.
func (r *Reloader) reload() (bool, error) {
	raw, err := r.files.read()
	if err != nil {
		return false, err
	}
	if sameBytes(raw, r.cur.raw) {
		return false, nil
	}
	m, err := r.files.parse(raw)
	if err != nil {
		return false, err
	}
	r.cur = m
	return true, nil
}

// load validates f, then reads and parses one generation of the files.
func (f Files) load() (*material, error) {
	if (f.Cert == "") != (f.Key == "") {
		return nil, ErrPartialPair
	}
	raw, err := f.read()
	if err != nil {
		return nil, err
	}
	return f.parse(raw)
}

func (f Files) read() ([3][]byte, error) {
	var raw [3][]byte
	for i, path := range [3]string{f.Cert, f.Key, f.CA} {
		if path == "" {
			continue
		}
		b, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			return raw, fmt.Errorf("tlsx: read %s: %w", path, err)
		}
		raw[i] = b
	}
	return raw, nil
}

func (f Files) parse(raw [3][]byte) (*material, error) {
	m := &material{raw: raw}
	if f.Cert != "" {
		cert, err := tls.X509KeyPair(raw[0], raw[1])
		if err != nil {
			return nil, fmt.Errorf("tlsx: parse %s + %s: %w", f.Cert, f.Key, err)
		}
		m.cert = &cert
	}
	if f.CA != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(raw[2]) {
			return nil, fmt.Errorf("%w: %s", ErrEmptyCA, f.CA)
		}
		m.pool = pool
	}
	return m, nil
}

// sameBytes compares contents, not mtimes: a same-size rewrite inside the
// filesystem's timestamp granularity still counts as a rotation.
func sameBytes(a, b [3][]byte) bool {
	return bytes.Equal(a[0], b[0]) && bytes.Equal(a[1], b[1]) && bytes.Equal(a[2], b[2])
}

// ParseClientAuth maps a config string to a client-certificate policy:
// none, request, require_any, verify_if_given, or require_and_verify. An empty
// mode means require_and_verify when a CA is configured and none otherwise.
// Verifying modes without a CA fail with [ErrNoClientCA], because an empty
// ClientCAs pool would make crypto/tls fall back to the system roots.
func ParseClientAuth(mode string, haveCA bool) (tls.ClientAuthType, error) {
	normalized := strings.ToLower(strings.TrimSpace(mode))
	if normalized == "" {
		if haveCA {
			return tls.RequireAndVerifyClientCert, nil
		}
		return tls.NoClientCert, nil
	}
	auth, err := clientAuthMode(normalized)
	if err != nil {
		return tls.NoClientCert, err
	}
	if verifiesClient(auth) && !haveCA {
		return tls.NoClientCert, ErrNoClientCA
	}
	return auth, nil
}

func clientAuthMode(mode string) (tls.ClientAuthType, error) {
	switch mode {
	case "none":
		return tls.NoClientCert, nil
	case "request":
		return tls.RequestClientCert, nil
	case "require_any":
		return tls.RequireAnyClientCert, nil
	case "verify_if_given":
		return tls.VerifyClientCertIfGiven, nil
	case "require_and_verify":
		return tls.RequireAndVerifyClientCert, nil
	default:
		return tls.NoClientCert, fmt.Errorf("%w: %q", ErrClientAuth, mode)
	}
}

func verifiesClient(auth tls.ClientAuthType) bool {
	return auth == tls.VerifyClientCertIfGiven || auth == tls.RequireAndVerifyClientCert
}
