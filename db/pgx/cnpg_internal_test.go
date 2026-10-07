// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pgx

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/secrets"
)

func TestWithConnParamsOverridesDSN(t *testing.T) {
	t.Parallel()
	params := [][2]string{{"sslmode", "disable"}, {"application_name", `it's a \ test`}}
	for name, dsn := range map[string]string{
		"url":          "postgres://app@db.example/app",
		"url query":    "postgresql://app@db.example/app?sslmode=require",
		"keyword":      "host=db.example user=app sslmode=require",
		"keyword tail": "host=db.example user=app   ",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg, err := pgconn.ParseConfig(withConnParams(dsn, params))
			if err != nil {
				t.Fatalf("ParseConfig: %v", err)
			}
			if cfg.TLSConfig != nil || len(cfg.Fallbacks) != 0 {
				t.Fatal("sslmode=disable from options did not override the DSN")
			}
			if got := cfg.RuntimeParams["application_name"]; got != `it's a \ test` {
				t.Fatalf("application_name = %q", got)
			}
		})
	}
	if got := withConnParams("host=x", nil); got != "host=x" {
		t.Fatalf("no params changed DSN to %q", got)
	}
}

func TestSSLOptionsParams(t *testing.T) {
	t.Parallel()
	if got := (SSLOptions{}).params(); len(got) != 0 || (SSLOptions{}).hasFiles() {
		t.Fatalf("zero SSLOptions params = %v", got)
	}
	opts := SSLOptions{Mode: "verify-full", Cert: "c", Key: "k"}
	if got := opts.params(); len(got) != 3 || got[0] != [2]string{"sslmode", "verify-full"} || !opts.hasFiles() {
		t.Fatalf("params = %v", got)
	}
}

func TestReadPasswordFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	got, err := readPasswordFile(t.Context(), write("password", "s3cret\n"))
	if err != nil || got != "s3cret" {
		t.Fatalf("readPasswordFile = %q, %v", got, err)
	}
	boundary := strings.Repeat("p", int(secrets.MaxFileBytes))
	if got, err = readPasswordFile(t.Context(), write("max", boundary)); err != nil || got != boundary {
		t.Fatalf("password at size limit rejected: %v", err)
	}
	if _, err = readPasswordFile(t.Context(), write("big", boundary+"p")); !errors.Is(err, secrets.ErrTooLarge) {
		t.Fatalf("oversized password error = %v", err)
	}
	if _, err = readPasswordFile(t.Context(), write("empty", "\n")); err == nil {
		t.Fatal("empty password accepted")
	}
	if _, err = readPasswordFile(t.Context(), filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing password file accepted")
	}
}

func TestBeforeConnectRereadsRotatedPassword(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "password")
	refresher := secretRefresher{passwordFile: path}
	cc := &pgx.ConnConfig{}
	for _, password := range []string{"first", "second"} {
		if err := os.WriteFile(path, []byte(password+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := refresher.beforeConnect(t.Context(), cc); err != nil {
			t.Fatalf("beforeConnect: %v", err)
		}
		if cc.Password != password {
			t.Fatalf("Password = %q, want %q", cc.Password, password)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := refresher.beforeConnect(t.Context(), cc); err == nil {
		t.Fatal("beforeConnect accepted a missing password file")
	}
}

func TestBeforeConnectReloadsRotatedRootCert(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.crt")
	writeCA(t, caPath, "ca-one")
	dsn := withConnParams("host=db.example user=app", SSLOptions{Mode: "verify-full", RootCert: caPath}.params())
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if err = applySecrets(t.Context(), cfg, dsn, Options{SSL: SSLOptions{RootCert: caPath}}); err != nil {
		t.Fatalf("applySecrets: %v", err)
	}
	if cfg.BeforeConnect == nil {
		t.Fatal("SSL file options installed no BeforeConnect hook")
	}
	first := cfg.ConnConfig.TLSConfig.RootCAs

	writeCA(t, caPath, "ca-two")
	cc := cfg.ConnConfig.Copy()
	if err = cfg.BeforeConnect(t.Context(), cc); err != nil {
		t.Fatalf("BeforeConnect: %v", err)
	}
	if cc.TLSConfig == nil || cc.TLSConfig.RootCAs.Equal(first) {
		t.Fatal("rotated root certificate was not reloaded")
	}

	if err = os.WriteFile(caPath, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = cfg.BeforeConnect(t.Context(), cfg.ConnConfig.Copy()); err == nil {
		t.Fatal("BeforeConnect accepted a corrupt root certificate")
	}
}

func TestApplySecretsWithoutOptionsKeepsConfig(t *testing.T) {
	t.Parallel()
	cfg, err := pgxpool.ParseConfig("host=db.example user=app password=dsn")
	if err != nil {
		t.Fatal(err)
	}
	if err = applySecrets(t.Context(), cfg, "", Options{}); err != nil || cfg.BeforeConnect != nil {
		t.Fatalf("applySecrets without options: %v, hook=%v", err, cfg.BeforeConnect != nil)
	}
	if cfg.ConnConfig.Password != "dsn" {
		t.Fatalf("Password = %q", cfg.ConnConfig.Password)
	}
}

func TestNewRejectsHalfClientCertificate(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	opts.DSN = "host=db.example user=app"
	opts.SSL = SSLOptions{Mode: "verify-full", Cert: filepath.Join(t.TempDir(), "client.crt")}
	_, err := New(t.Context(), opts, discardLogger(), clock.NewFake())
	if err == nil || !strings.Contains(err.Error(), "sslkey") {
		t.Fatalf("New error = %v, want sslcert/sslkey pairing error before any connect", err)
	}
}

func TestReadPoolRequiresDSN(t *testing.T) {
	t.Parallel()
	if _, err := NewReadPool(t.Context(), DefaultOptions(), discardLogger(), clock.NewFake()); err == nil {
		t.Fatal("NewReadPool accepted an empty read DSN")
	}
	primary := &pgxpool.Pool{}
	read, err := provideReadPool(fxtest.NewLifecycle(t), Options{}, primary, discardLogger(), clock.NewFake())
	if err != nil || read.Pool != primary {
		t.Fatalf("unset read_dsn must share the primary pool: %v", err)
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func writeCA(t *testing.T, path, commonName string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             now,
		NotAfter:              now.AddDate(10, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}
