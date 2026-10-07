// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package smtpserver

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"log/slog"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/config"
)

type lifecycleRecorder struct {
	hook fx.Hook
}

func (l *lifecycleRecorder) Append(hook fx.Hook) {
	l.hook = hook
}

func TestStartServerFailsFxStartupWhenBindFails(t *testing.T) {
	t.Parallel()
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy address: %v", err)
	}
	t.Cleanup(func() { _ = occupied.Close() })

	lifecycle := &lifecycleRecorder{}
	err = startServer(params{
		LC:      lifecycle,
		Cfg:     Config{Addr: occupied.Addr().String()},
		Backend: NewHandlerBackend(func(Envelope) error { return nil }),
		Logger:  slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("startServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := lifecycle.hook.OnStart(ctx); err == nil || !strings.Contains(err.Error(), "listen") {
		t.Fatalf("OnStart bind error = %v, want listen failure", err)
	}
}

func TestStartServerBindsBeforeFxStartupSucceeds(t *testing.T) {
	t.Parallel()
	lifecycle := &lifecycleRecorder{}
	if err := startServer(params{
		LC:      lifecycle,
		Cfg:     Config{Addr: "127.0.0.1:0"},
		Backend: NewHandlerBackend(func(Envelope) error { return nil }),
		Logger:  slog.New(slog.DiscardHandler),
	}); err != nil {
		t.Fatalf("startServer: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := lifecycle.hook.OnStart(ctx); err != nil {
		t.Fatalf("OnStart: %v", err)
	}
	if err := lifecycle.hook.OnStop(ctx); err != nil {
		t.Fatalf("OnStop: %v", err)
	}
}

func TestWithDefaults_zero(t *testing.T) {
	t.Parallel()
	c := Config{}.withDefaults()
	if c.Addr != defaultAddr {
		t.Errorf("Addr = %q, want %q", c.Addr, defaultAddr)
	}
	if c.Domain != defaultDomain {
		t.Errorf("Domain = %q, want %q", c.Domain, defaultDomain)
	}
	if c.MaxMessageBytes != defaultMaxMessageBytes {
		t.Errorf("MaxMessageBytes = %d", c.MaxMessageBytes)
	}
	if c.MaxRecipients != defaultMaxRecipients {
		t.Errorf("MaxRecipients = %d", c.MaxRecipients)
	}
}

func TestWithDefaults_preserves(t *testing.T) {
	t.Parallel()
	c := Config{Addr: ":2525", Domain: "example.com", MaxRecipients: 5}.withDefaults()
	if c.Addr != ":2525" {
		t.Errorf("Addr = %q", c.Addr)
	}
	if c.MaxRecipients != 5 {
		t.Errorf("MaxRecipients = %d", c.MaxRecipients)
	}
}

func TestLoadConfig_defaults(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(cfg)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if c.Addr != defaultAddr {
		t.Errorf("Addr = %q", c.Addr)
	}
}

func TestLoadConfigDefaultEnvPaths(t *testing.T) {
	t.Setenv("TEST_SMTP_TLS_CERT_FILE", "/run/tls/server.crt")
	t.Setenv("TEST_SMTP_TLS_KEY_FILE", "/run/tls/server.key")
	t.Setenv("TEST_SMTP_IMPLICIT_TLS", "true")
	t.Setenv("TEST_SMTP_ALLOW_INSECURE_AUTH", "true")
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(cfg)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if c.TLSCertFile != "/run/tls/server.crt" || c.TLSKeyFile != "/run/tls/server.key" ||
		!c.ImplicitTLS || !c.AllowInsecureAuth {
		t.Fatalf("TLS config not loaded from APP_SMTP_* shape: %+v", c)
	}
}

func TestNewServerDisablesPlaintextAuthByDefault(t *testing.T) {
	t.Parallel()
	srv, err := newServer(NewHandlerBackend(func(Envelope) error { return nil }), DefaultConfig())
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	if srv.AllowInsecureAuth {
		t.Fatal("plaintext AUTH enabled by default")
	}
}

func TestNewServerRejectsPartialTLSConfig(t *testing.T) {
	t.Parallel()
	_, err := newServer(NewHandlerBackend(func(Envelope) error { return nil }), Config{TLSCertFile: "cert.pem"})
	if err == nil || !strings.Contains(err.Error(), "both") {
		t.Fatalf("newServer partial TLS config = %v, want paired-file error", err)
	}
}

func TestNewServerRejectsImplicitTLSWithoutCertificate(t *testing.T) {
	t.Parallel()
	_, err := newServer(NewHandlerBackend(func(Envelope) error { return nil }), Config{ImplicitTLS: true})
	if err == nil || !strings.Contains(err.Error(), "implicit TLS") {
		t.Fatalf("newServer implicit TLS without certificate = %v", err)
	}
}

func TestNewServerLoadsTLSCertificate(t *testing.T) {
	t.Parallel()
	certFile, keyFile := writeTLSFixture(t)
	srv, err := newServer(NewHandlerBackend(func(Envelope) error { return nil }), Config{
		TLSCertFile: certFile,
		TLSKeyFile:  keyFile,
	})
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	if srv.TLSConfig == nil || srv.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Fatal("TLS configuration missing or below TLS 1.2")
	}
}

func writeTLSFixture(t *testing.T) (string, string) {
	t.Helper()
	tlsServer := httptest.NewTLSServer(nil)
	t.Cleanup(tlsServer.Close)
	certificate := tlsServer.TLS.Certificates[0]

	var certPEM bytes.Buffer
	for _, der := range certificate.Certificate {
		if err := pem.Encode(&certPEM, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
			t.Fatalf("encode certificate: %v", err)
		}
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(certificate.PrivateKey)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	dir := t.TempDir()
	certFile := filepath.Join(dir, "server.crt")
	keyFile := filepath.Join(dir, "server.key")
	if err := os.WriteFile(certFile, certPEM.Bytes(), 0o600); err != nil {
		t.Fatalf("write certificate: %v", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatalf("write private key: %v", err)
	}
	return certFile, keyFile
}
