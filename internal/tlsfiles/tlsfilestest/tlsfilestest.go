// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package tlsfilestest writes a throwaway CA and a client certificate signed
// by it as PEM files for tests of file-based TLS configuration.
package tlsfilestest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Paths locates the PEM files written by [Write].
type Paths struct {
	CA   string
	Cert string
	Key  string
}

// validity spans the test run; the fixed start keeps the helper clock-free.
var notBefore = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// Write creates ca.pem, cert.pem, and key.pem in a fresh temporary directory.
func Write(t *testing.T) Paths {
	t.Helper()
	dir := t.TempDir()
	caKey := newKey(t)
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "tlsfilestest CA"},
		NotBefore: notBefore, NotAfter: notBefore.AddDate(100, 0, 0),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER := sign(t, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	leafKey := newKey(t)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "tlsfilestest client"},
		NotBefore: notBefore, NotAfter: notBefore.AddDate(100, 0, 0),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, KeyUsage: x509.KeyUsageDigitalSignature,
	}
	leafDER := sign(t, leafTmpl, caTmpl, &leafKey.PublicKey, caKey)
	keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		t.Fatalf("tlsfilestest: marshal key: %v", err)
	}
	paths := Paths{
		CA:   filepath.Join(dir, "ca.pem"),
		Cert: filepath.Join(dir, "cert.pem"),
		Key:  filepath.Join(dir, "key.pem"),
	}
	writePEM(t, paths.CA, "CERTIFICATE", caDER)
	writePEM(t, paths.Cert, "CERTIFICATE", leafDER)
	writePEM(t, paths.Key, "PRIVATE KEY", keyDER)
	return paths
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("tlsfilestest: generate key: %v", err)
	}
	return key
}

func sign(t *testing.T, tmpl, parent *x509.Certificate, pub any, signer *ecdsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, signer)
	if err != nil {
		t.Fatalf("tlsfilestest: create certificate: %v", err)
	}
	return der
}

func writePEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	data := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("tlsfilestest: write %s: %v", path, err)
	}
}
