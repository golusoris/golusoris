// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package tlsxtest_test

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/golusoris/golusoris/core/tlsx/tlsxtest"
)

// recordingTB captures Fatalf instead of stopping the test.
type recordingTB struct {
	testing.TB
	failures []string
}

func (r *recordingTB) Helper() {}

func (r *recordingTB) Fatalf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

func verify(t *testing.T, ca *tlsxtest.CA, p tlsxtest.Pair, opts x509.VerifyOptions) error {
	t.Helper()
	pair, err := tls.X509KeyPair(p.CertPEM, p.KeyPEM)
	if err != nil {
		t.Fatalf("key pair: %v", err)
	}
	opts.Roots = x509.NewCertPool()
	if !opts.Roots.AppendCertsFromPEM(ca.PEM) {
		t.Fatal("CA PEM holds no certificate")
	}
	_, err = pair.Leaf.Verify(opts)
	return err
}

func TestCA_IssuesVerifiableLeaves(t *testing.T) {
	t.Parallel()
	ca := tlsxtest.NewCA(t)
	server := ca.Server(t, "127.0.0.1", "svc.local")
	client := ca.Client(t, "worker")

	if err := verify(t, ca, server, x509.VerifyOptions{DNSName: "svc.local"}); err != nil {
		t.Fatalf("server leaf for DNS name: %v", err)
	}
	if err := verify(t, ca, server, x509.VerifyOptions{DNSName: "127.0.0.1"}); err != nil {
		t.Fatalf("server leaf for IP: %v", err)
	}
	clientUsage := []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	if err := verify(t, ca, client, x509.VerifyOptions{KeyUsages: clientUsage}); err != nil {
		t.Fatalf("client leaf: %v", err)
	}
	if server.Serial.Cmp(client.Serial) == 0 {
		t.Fatal("two leaves share a serial")
	}
}

func TestCA_LeavesAreScoped(t *testing.T) {
	t.Parallel()
	ca := tlsxtest.NewCA(t)
	if err := verify(t, ca, ca.Server(t, "svc.local"), x509.VerifyOptions{DNSName: "other.local"}); err == nil {
		t.Fatal("server leaf verified for a foreign host")
	}
	if err := verify(t, ca, ca.Client(t, "worker"), x509.VerifyOptions{}); err == nil {
		t.Fatal("client leaf verified for server auth")
	}
	foreign := tlsxtest.NewCA(t)
	if err := verify(t, foreign, ca.Server(t, "svc.local"), x509.VerifyOptions{DNSName: "svc.local"}); err == nil {
		t.Fatal("leaf verified against a foreign CA")
	}
}

func TestCA_ServerWithoutHosts(t *testing.T) {
	t.Parallel()
	ca := tlsxtest.NewCA(t)
	leaf := ca.Server(t)
	if err := verify(t, ca, leaf, x509.VerifyOptions{}); err != nil {
		t.Fatalf("host-less server leaf: %v", err)
	}
	// Each issuance carries its own key.
	if _, err := tls.X509KeyPair(leaf.CertPEM, ca.Server(t).KeyPEM); err == nil {
		t.Fatal("certificate paired with another issuance's key")
	}
}

func TestWriteFilesAndRotate(t *testing.T) {
	t.Parallel()
	ca := tlsxtest.NewCA(t)
	dir := t.TempDir()
	first := ca.Server(t, "localhost")
	files := ca.WriteFiles(t, dir, first)
	assertFile(t, files.Cert, first.CertPEM)
	assertFile(t, files.Key, first.KeyPEM)
	assertFile(t, files.CA, ca.PEM)

	second := ca.Server(t, "localhost")
	tlsxtest.WritePair(t, files, second)
	assertFile(t, files.Cert, second.CertPEM)
	assertFile(t, files.Key, second.KeyPEM)

	rotated := tlsxtest.NewCA(t)
	rotated.WriteCA(t, files.CA)
	assertFile(t, files.CA, rotated.PEM)
}

func TestWriteFiles_ReportsWriteFailure(t *testing.T) {
	t.Parallel()
	ca := tlsxtest.NewCA(t)
	rec := &recordingTB{TB: t}
	ca.WriteFiles(rec, filepath.Join(t.TempDir(), "missing-dir"), ca.Server(t, "localhost"))
	if len(rec.failures) != 3 {
		t.Fatalf("failures = %q, want one per file", rec.failures)
	}
}

func assertFile(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s holds unexpected bytes", path)
	}
}
