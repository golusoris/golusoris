// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package tlsxtest issues throwaway ECDSA certificates for TLS tests. Keys are
// generated at test time and written only below the caller's directory, so no
// key material is ever committed.
//
//	ca := tlsxtest.NewCA(t)
//	files := ca.WriteFiles(t, t.TempDir(), ca.Server(t, "127.0.0.1", "localhost"))
//	r, err := tlsx.NewReloader(files, tlsx.Options{})
package tlsxtest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golusoris/golusoris/core/tlsx"
)

// Fixed validity window: certificates stay valid for any test run date
// without reading the wall clock.
var (
	notBefore = time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
	notAfter  = time.Date(2100, time.January, 1, 0, 0, 0, 0, time.UTC)
)

// CA is an in-memory certificate authority.
type CA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	// PEM is the CA certificate, PEM-encoded.
	PEM []byte
}

// Pair is an issued leaf certificate and its private key.
type Pair struct {
	CertPEM []byte
	KeyPEM  []byte
	// Serial identifies the leaf, so tests can tell rotations apart.
	Serial *big.Int
}

// NewCA creates a self-signed CA.
func NewCA(tb testing.TB) *CA {
	tb.Helper()
	key := newKey(tb)
	tmpl := &x509.Certificate{
		SerialNumber:          newSerial(tb),
		Subject:               pkix.Name{CommonName: "tlsxtest CA"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		tb.Fatalf("tlsxtest: create CA: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		tb.Fatalf("tlsxtest: parse CA: %v", err)
	}
	return &CA{cert: cert, key: key, PEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// Server issues a server-auth leaf for hosts (IP literals or DNS names).
func (ca *CA) Server(tb testing.TB, hosts ...string) Pair {
	tb.Helper()
	tmpl := ca.leafTemplate(tb, "tlsxtest server", x509.ExtKeyUsageServerAuth)
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
			continue
		}
		tmpl.DNSNames = append(tmpl.DNSNames, h)
	}
	return ca.sign(tb, tmpl)
}

// Client issues a client-auth leaf with common name name.
func (ca *CA) Client(tb testing.TB, name string) Pair {
	tb.Helper()
	return ca.sign(tb, ca.leafTemplate(tb, name, x509.ExtKeyUsageClientAuth))
}

// WriteFiles writes the CA as ca.pem and p as cert.pem + key.pem into dir.
func (ca *CA) WriteFiles(tb testing.TB, dir string, p Pair) tlsx.Files {
	tb.Helper()
	files := tlsx.Files{
		Cert: filepath.Join(dir, "cert.pem"),
		Key:  filepath.Join(dir, "key.pem"),
		CA:   filepath.Join(dir, "ca.pem"),
	}
	writeFile(tb, files.CA, ca.PEM)
	WritePair(tb, files, p)
	return files
}

// WriteCA overwrites path with the CA certificate, as a trust-bundle rotation does.
func (ca *CA) WriteCA(tb testing.TB, path string) {
	tb.Helper()
	writeFile(tb, path, ca.PEM)
}

// WritePair overwrites the cert and key files named by files with p.
func WritePair(tb testing.TB, files tlsx.Files, p Pair) {
	tb.Helper()
	writeFile(tb, files.Cert, p.CertPEM)
	writeFile(tb, files.Key, p.KeyPEM)
}

func (ca *CA) leafTemplate(tb testing.TB, name string, usage x509.ExtKeyUsage) *x509.Certificate {
	tb.Helper()
	return &x509.Certificate{
		SerialNumber: newSerial(tb),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
	}
}

func (ca *CA) sign(tb testing.TB, tmpl *x509.Certificate) Pair {
	tb.Helper()
	key := newKey(tb)
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		tb.Fatalf("tlsxtest: sign leaf: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		tb.Fatalf("tlsxtest: marshal key: %v", err)
	}
	return Pair{
		CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		KeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
		Serial:  tmpl.SerialNumber,
	}
}

func newKey(tb testing.TB) *ecdsa.PrivateKey {
	tb.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		tb.Fatalf("tlsxtest: generate key: %v", err)
	}
	return key
}

func newSerial(tb testing.TB) *big.Int {
	tb.Helper()
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		tb.Fatalf("tlsxtest: serial: %v", err)
	}
	return serial
}

func writeFile(tb testing.TB, path string, data []byte) {
	tb.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		tb.Fatalf("tlsxtest: write %s: %v", path, err)
	}
}
