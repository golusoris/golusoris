// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package tlsfiles builds a client *tls.Config from PEM files for broker
// clients such as pubsub/nats and pubsub/kafka. Files are read once.
package tlsfiles

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var (
	// ErrPartialPair reports a certificate without its key or a key without its certificate.
	ErrPartialPair = errors.New("tlsfiles: cert and key files must be set together")
	// ErrEmptyCA reports a CA file without any PEM certificate.
	ErrEmptyCA = errors.New("tlsfiles: CA file holds no PEM certificate")
)

// Files names PEM files. CA alone verifies the server with a private CA;
// Cert and Key add a client certificate.
type Files struct {
	CA   string
	Cert string
	Key  string
}

// IsZero reports whether no file is configured.
func (f Files) IsZero() bool { return f == Files{} }

// ClientConfig loads f into a client configuration with TLS 1.2 as the floor.
// Without a CA file the system roots verify the server.
func ClientConfig(f Files) (*tls.Config, error) {
	if (f.Cert == "") != (f.Key == "") {
		return nil, ErrPartialPair
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if f.CA != "" {
		pool, err := loadPool(f.CA)
		if err != nil {
			return nil, err
		}
		cfg.RootCAs = pool
	}
	if f.Cert != "" {
		pair, err := tls.LoadX509KeyPair(filepath.Clean(f.Cert), filepath.Clean(f.Key))
		if err != nil {
			return nil, fmt.Errorf("tlsfiles: load key pair: %w", err)
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	return cfg, nil
}

func loadPool(path string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("tlsfiles: read CA file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("%w: %s", ErrEmptyCA, path)
	}
	return pool, nil
}
