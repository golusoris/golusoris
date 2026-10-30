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
	"net/smtp"
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

func TestReadyListener_Accept(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	ready := make(chan struct{})
	rl := &readyListener{Listener: ln, ready: ready}

	go func() {
		conn, dialErr := net.Dial("tcp", ln.Addr().String())
		if dialErr == nil {
			_ = conn.Close()
		}
	}()

	conn, err := rl.Accept()
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	_ = conn.Close()

	select {
	case <-ready:
	default:
		t.Fatal("expected ready channel to be closed after Accept")
	}
}

// assertSessionMailAndRcpt verifies Mail and multiple Rcpt calls populate envelope fields.
func assertSessionMailAndRcpt(t *testing.T, sess *handlerSession) {
	t.Helper()
	if err := sess.Mail("sender@example.com", nil); err != nil {
		t.Fatalf("Mail: %v", err)
	}
	if sess.env.From != "sender@example.com" {
		t.Errorf("env.From = %q, want %q", sess.env.From, "sender@example.com")
	}

	if err := sess.Rcpt("rcpt1@example.com", nil); err != nil {
		t.Fatalf("Rcpt 1: %v", err)
	}
	if err := sess.Rcpt("rcpt2@example.com", nil); err != nil {
		t.Fatalf("Rcpt 2: %v", err)
	}
	if len(sess.env.To) != 2 || sess.env.To[0] != "rcpt1@example.com" || sess.env.To[1] != "rcpt2@example.com" {
		t.Errorf("env.To = %v", sess.env.To)
	}
}

// assertSessionLogoutAndReset verifies Logout and Reset behavior on envelope state.
func assertSessionLogoutAndReset(t *testing.T, sess *handlerSession) {
	t.Helper()
	if err := sess.Logout(); err != nil {
		t.Errorf("Logout: %v", err)
	}

	sess.Reset()
	if sess.env.From != "" || len(sess.env.To) != 0 {
		t.Errorf("Reset did not clear envelope: %+v", sess.env)
	}
}

func TestHandlerSession_Methods(t *testing.T) {
	t.Parallel()
	sess := &handlerSession{handler: func(Envelope) error { return nil }}
	assertSessionMailAndRcpt(t, sess)
	assertSessionLogoutAndReset(t, sess)
}

// startTestSMTPServer starts an in-process SMTP server with bounded message bytes and recipients.
func startTestSMTPServer(t *testing.T) (string, <-chan Envelope) {
	t.Helper()
	delivered := make(chan Envelope, 1)
	backend := NewHandlerBackend(func(env Envelope) error {
		delivered <- env
		return nil
	})

	cfg := Config{
		Addr:            "127.0.0.1:0",
		Domain:          "localhost",
		MaxMessageBytes: 50,
		MaxRecipients:   1,
	}
	srv, err := newServer(backend, cfg)
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close(); _ = ln.Close() })

	go func() {
		_ = srv.Serve(ln)
	}()
	return ln.Addr().String(), delivered
}

// sendTestSMTPMessage performs the full SMTP ceremony to send a test message.
func sendTestSMTPMessage(t *testing.T, addr string) {
	t.Helper()
	client, dialErr := smtp.Dial(addr)
	if dialErr != nil {
		t.Fatalf("dial: %v", dialErr)
	}
	if mailErr := client.Mail("alice@example.com"); mailErr != nil {
		t.Fatalf("Mail: %v", mailErr)
	}
	if rcptErr := client.Rcpt("bob@example.com"); rcptErr != nil {
		t.Fatalf("Rcpt: %v", rcptErr)
	}
	wc, dataErr := client.Data()
	if dataErr != nil {
		t.Fatalf("Data: %v", dataErr)
	}
	msg := []byte("Subject: Hi\r\n\r\nHello")
	if _, writeErr := wc.Write(msg); writeErr != nil {
		t.Fatalf("Write: %v", writeErr)
	}
	if closeErr := wc.Close(); closeErr != nil {
		t.Fatalf("Close data: %v", closeErr)
	}
	if quitErr := client.Quit(); quitErr != nil {
		t.Fatalf("Quit: %v", quitErr)
	}
}

// assertSMTPDelivery verifies that the expected envelope was received by the backend.
func assertSMTPDelivery(t *testing.T, delivered <-chan Envelope) {
	t.Helper()
	select {
	case env := <-delivered:
		if env.From != "alice@example.com" {
			t.Errorf("From = %q", env.From)
		}
		if len(env.To) != 1 || env.To[0] != "bob@example.com" {
			t.Errorf("To = %v", env.To)
		}
		if string(env.Data) != "Subject: Hi\r\n\r\nHello\r\n" {
			t.Errorf("Data = %q", string(env.Data))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for message delivery")
	}
}

// testSMTPFullCeremony tests positive full SMTP ceremony and delivery verification.
func testSMTPFullCeremony(t *testing.T, addr string, delivered <-chan Envelope) {
	t.Helper()
	sendTestSMTPMessage(t, addr)
	assertSMTPDelivery(t, delivered)
}

// testSMTPRecipientLimitAndReset tests recipient limit rejection and reset recovery.
func testSMTPRecipientLimitAndReset(t *testing.T, client *smtp.Client) {
	t.Helper()
	if mailErr := client.Mail("alice@example.com"); mailErr != nil {
		t.Fatalf("Mail: %v", mailErr)
	}
	if rcpt1Err := client.Rcpt("rcpt1@example.com"); rcpt1Err != nil {
		t.Fatalf("Rcpt 1: %v", rcpt1Err)
	}
	if rcpt2Err := client.Rcpt("rcpt2@example.com"); rcpt2Err == nil {
		t.Fatal("expected error on exceeding MaxRecipients, got nil")
	}
	if resetErr := client.Reset(); resetErr != nil {
		t.Fatalf("Reset: %v", resetErr)
	}
}

// testSMTPOversizedMessage tests message size boundary rejection.
func testSMTPOversizedMessage(t *testing.T, client *smtp.Client) {
	t.Helper()
	if mailAfterResetErr := client.Mail("alice@example.com"); mailAfterResetErr != nil {
		t.Fatalf("Mail after reset: %v", mailAfterResetErr)
	}
	if rcptAfterResetErr := client.Rcpt("rcpt1@example.com"); rcptAfterResetErr != nil {
		t.Fatalf("Rcpt after reset: %v", rcptAfterResetErr)
	}
	wc2, data2Err := client.Data()
	if data2Err != nil {
		t.Fatalf("Data: %v", data2Err)
	}
	oversized := bytes.Repeat([]byte("A"), 100)
	_, writeErr := wc2.Write(oversized)
	closeErr := wc2.Close()
	if writeErr == nil && closeErr == nil {
		t.Fatal("expected error for oversized message, got nil")
	}
}

func TestSMTPServer_ClientCeremonyAndBoundaries(t *testing.T) {
	t.Parallel()
	addr, delivered := startTestSMTPServer(t)
	testSMTPFullCeremony(t, addr, delivered)

	client2, dial2Err := smtp.Dial(addr)
	if dial2Err != nil {
		t.Fatalf("dial: %v", dial2Err)
	}
	defer client2.Close()

	testSMTPRecipientLimitAndReset(t, client2)
	testSMTPOversizedMessage(t, client2)
}
