// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package tlsx_test

import (
	"context"
	"crypto/tls"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/tlsx"
	"github.com/golusoris/golusoris/core/tlsx/tlsxtest"
)

const (
	interval = 10 * time.Second
	host     = "127.0.0.1"
)

// result is what each side of one handshake saw.
type result struct {
	serverSerial *big.Int // leaf the client saw
	clientSerial *big.Int // leaf the server saw; nil when none was sent
	err          error
}

// handshake runs one TLS handshake over loopback TCP and exchanges one byte, so
// a server-side rejection of the client certificate reaches the client.
func handshake(t *testing.T, srvCfg, cliCfg *tls.Config) result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", host+":0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	seen := make(chan *big.Int, 1)
	go serveOne(ctx, ln, srvCfg, seen)

	dialer := &tls.Dialer{Config: cliCfg}
	conn, err := dialer.DialContext(ctx, "tcp", ln.Addr().String())
	if err != nil {
		<-seen
		return result{err: err}
	}
	defer func() { _ = conn.Close() }()
	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		t.Fatalf("dialer returned %T", conn)
	}
	if _, err = tlsConn.Read(make([]byte, 1)); err != nil {
		<-seen
		return result{err: err}
	}
	return result{serverSerial: tlsConn.ConnectionState().PeerCertificates[0].SerialNumber, clientSerial: <-seen}
}

// serveOne accepts one connection, reports the client leaf serial, and writes one byte.
func serveOne(ctx context.Context, ln net.Listener, cfg *tls.Config, seen chan<- *big.Int) {
	conn, err := ln.Accept()
	if err != nil {
		seen <- nil
		return
	}
	defer func() { _ = conn.Close() }()
	srv := tls.Server(conn, cfg)
	if srv.HandshakeContext(ctx) != nil {
		seen <- nil
		return
	}
	var peer *big.Int
	if certs := srv.ConnectionState().PeerCertificates; len(certs) > 0 {
		peer = certs[0].SerialNumber
	}
	seen <- peer
	_, _ = srv.Write([]byte{1})
}

func newReloader(t *testing.T, files tlsx.Files, clk clock.Clock) *tlsx.Reloader {
	t.Helper()
	r, err := tlsx.NewReloader(files, tlsx.Options{MinInterval: interval, Clock: clk})
	if err != nil {
		t.Fatalf("NewReloader: %v", err)
	}
	return r
}

// clientFor builds a client reloader trusting ca and presenting a fresh client leaf.
func clientFor(t *testing.T, ca *tlsxtest.CA, clk clock.Clock) *tlsx.Reloader {
	t.Helper()
	return newReloader(t, ca.WriteFiles(t, t.TempDir(), ca.Client(t, "client")), clk)
}

func wantServer(t *testing.T, got result, want *big.Int) {
	t.Helper()
	if got.err != nil {
		t.Fatalf("handshake: %v", got.err)
	}
	if got.serverSerial.Cmp(want) != 0 {
		t.Fatalf("server serial = %v, want %v", got.serverSerial, want)
	}
}

func wantFailure(t *testing.T, got result, what string) {
	t.Helper()
	if got.err == nil {
		t.Fatalf("%s: handshake succeeded, want failure", what)
	}
}

func TestReloader_ServerRotation(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake()
	ca := tlsxtest.NewCA(t)
	first := ca.Server(t, host)
	files := ca.WriteFiles(t, t.TempDir(), first)
	srv := newReloader(t, files, clk)
	cli := clientFor(t, ca, clk)

	wantServer(t, handshake(t, srv.ServerConfig(tls.NoClientCert), cli.ClientConfig(host)), first.Serial)

	second := ca.Server(t, host)
	tlsxtest.WritePair(t, files, second)

	// Boundary: one nanosecond short of MinInterval keeps the old leaf.
	clk.Advance(interval - time.Nanosecond)
	wantServer(t, handshake(t, srv.ServerConfig(tls.NoClientCert), cli.ClientConfig(host)), first.Serial)

	// Exactly MinInterval since the last read picks up the rotation.
	clk.Advance(time.Nanosecond)
	wantServer(t, handshake(t, srv.ServerConfig(tls.NoClientCert), cli.ClientConfig(host)), second.Serial)
	if err := srv.LastError(); err != nil {
		t.Fatalf("LastError = %v, want nil", err)
	}
}

func TestReloader_BadRotationKeepsLastGood(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake()
	ca := tlsxtest.NewCA(t)
	first := ca.Server(t, host)
	files := ca.WriteFiles(t, t.TempDir(), first)
	srv := newReloader(t, files, clk)
	cli := clientFor(t, ca, clk)
	cfg := srv.ServerConfig(tls.NoClientCert)

	// Half-written rotation: new certificate, old key.
	if err := os.WriteFile(files.Cert, ca.Server(t, host).CertPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	clk.Advance(interval)
	wantServer(t, handshake(t, cfg, cli.ClientConfig(host)), first.Serial)
	if srv.LastError() == nil {
		t.Fatal("LastError = nil after a bad rotation")
	}

	// The next good write clears the error.
	third := ca.Server(t, host)
	tlsxtest.WritePair(t, files, third)
	clk.Advance(interval)
	wantServer(t, handshake(t, cfg, cli.ClientConfig(host)), third.Serial)
	if err := srv.LastError(); err != nil {
		t.Fatalf("LastError = %v after a good rotation", err)
	}
}

func TestReloader_MissingFileAfterStartKeepsLastGood(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake()
	ca := tlsxtest.NewCA(t)
	first := ca.Server(t, host)
	files := ca.WriteFiles(t, t.TempDir(), first)
	srv := newReloader(t, files, clk)
	cli := clientFor(t, ca, clk)

	if err := os.Remove(files.Key); err != nil {
		t.Fatal(err)
	}
	clk.Advance(interval)
	wantServer(t, handshake(t, srv.ServerConfig(tls.NoClientCert), cli.ClientConfig(host)), first.Serial)
	if err := srv.LastError(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("LastError = %v, want os.ErrNotExist", err)
	}
}

func TestReloader_MutualTLS(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake()
	ca := tlsxtest.NewCA(t)
	srv := newReloader(t, ca.WriteFiles(t, t.TempDir(), ca.Server(t, host)), clk)
	cfg := srv.ServerConfig(tls.RequireAndVerifyClientCert)

	clientPair := ca.Client(t, "client")
	cli := newReloader(t, ca.WriteFiles(t, t.TempDir(), clientPair), clk)
	got := handshake(t, cfg, cli.ClientConfig(host))
	if got.err != nil || got.clientSerial.Cmp(clientPair.Serial) != 0 {
		t.Fatalf("mTLS handshake: err=%v client serial=%v, want %v", got.err, got.clientSerial, clientPair.Serial)
	}

	caOnly := newReloader(t, tlsx.Files{CA: writeFile(t, ca.PEM)}, clk)
	wantFailure(t, handshake(t, cfg, caOnly.ClientConfig(host)), "client without certificate")

	other := tlsxtest.NewCA(t)
	foreign := other.WriteFiles(t, t.TempDir(), other.Client(t, "intruder"))
	ca.WriteCA(t, foreign.CA) // trusts the real server, presents a foreign leaf
	intruder := newReloader(t, foreign, clk)
	wantFailure(t, handshake(t, cfg, intruder.ClientConfig(host)), "client from a foreign CA")
}

func TestReloader_ClientCertificateRotation(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake()
	ca := tlsxtest.NewCA(t)
	srv := newReloader(t, ca.WriteFiles(t, t.TempDir(), ca.Server(t, host)), clk)
	files := ca.WriteFiles(t, t.TempDir(), ca.Client(t, "client"))
	cli := newReloader(t, files, clk)
	cliCfg := cli.ClientConfig(host)

	second := ca.Client(t, "client")
	tlsxtest.WritePair(t, files, second)
	clk.Advance(interval)
	// The config built before the rotation presents the rotated leaf.
	got := handshake(t, srv.ServerConfig(tls.RequireAndVerifyClientCert), cliCfg)
	if got.err != nil || got.clientSerial.Cmp(second.Serial) != 0 {
		t.Fatalf("err=%v client serial=%v, want %v", got.err, got.clientSerial, second.Serial)
	}
}

func TestReloader_CARotation(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake()
	oldCA, newCA := tlsxtest.NewCA(t), tlsxtest.NewCA(t)
	srv := newReloader(t, newCA.WriteFiles(t, t.TempDir(), newCA.Server(t, host)), clk)
	caFile := writeFile(t, oldCA.PEM)
	cli := newReloader(t, tlsx.Files{CA: caFile}, clk)
	cfg := srv.ServerConfig(tls.NoClientCert)

	wantFailure(t, handshake(t, cfg, cli.ClientConfig(host)), "server from an untrusted CA")
	newCA.WriteCA(t, caFile)
	clk.Advance(interval)
	if got := handshake(t, cfg, cli.ClientConfig(host)); got.err != nil {
		t.Fatalf("after CA rotation: %v", got.err)
	}
}

func TestReloader_ServerConfigNeedsMaterial(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake()
	ca := tlsxtest.NewCA(t)
	cli := clientFor(t, ca, clk)

	caOnly := newReloader(t, tlsx.Files{CA: writeFile(t, ca.PEM)}, clk)
	caOnlyCfg := caOnly.ServerConfig(tls.NoClientCert)
	wantFailure(t, handshake(t, caOnlyCfg, cli.ClientConfig(host)), "server without certificate")
	if _, err := caOnlyCfg.GetConfigForClient(&tls.ClientHelloInfo{}); !errors.Is(err, tlsx.ErrNoCertificate) {
		t.Fatalf("CA-only server config: err = %v, want ErrNoCertificate", err)
	}

	files := ca.WriteFiles(t, t.TempDir(), ca.Server(t, host))
	noCA := newReloader(t, tlsx.Files{Cert: files.Cert, Key: files.Key}, clk)
	noCACfg := noCA.ServerConfig(tls.RequireAndVerifyClientCert)
	wantFailure(t, handshake(t, noCACfg, cli.ClientConfig(host)), "verify without CA")
	// Without the guard crypto/tls would verify clients against the system roots.
	if _, err := noCACfg.GetConfigForClient(&tls.ClientHelloInfo{}); !errors.Is(err, tlsx.ErrNoClientCA) {
		t.Fatalf("verify without CA: err = %v, want ErrNoClientCA", err)
	}
	if _, err := noCA.ServerConfig(tls.RequestClientCert).GetConfigForClient(&tls.ClientHelloInfo{}); err != nil {
		t.Fatalf("non-verifying mode without CA: %v", err)
	}
}

// rejectCase is a file set a loader must refuse; a nil want accepts any error.
type rejectCase struct {
	name  string
	files tlsx.Files
	want  error
}

// rejectCases returns valid files plus the file sets every loader refuses.
func rejectCases(t *testing.T) (tlsx.Files, []rejectCase) {
	t.Helper()
	ca := tlsxtest.NewCA(t)
	dir := t.TempDir()
	good := ca.WriteFiles(t, dir, ca.Server(t, "localhost"))
	garbage := filepath.Join(dir, "garbage.pem")
	if err := os.WriteFile(garbage, []byte("not pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing.pem")
	otherKey := writeFile(t, ca.Server(t, "localhost").KeyPEM)
	return good, []rejectCase{
		{"cert without key", tlsx.Files{Cert: good.Cert}, tlsx.ErrPartialPair},
		{"key without cert", tlsx.Files{Key: good.Key, CA: good.CA}, tlsx.ErrPartialPair},
		{"key alone", tlsx.Files{Key: good.Key}, tlsx.ErrPartialPair},
		{"missing cert", tlsx.Files{Cert: missing, Key: good.Key}, os.ErrNotExist},
		{"missing CA", tlsx.Files{CA: missing}, os.ErrNotExist},
		{"empty CA", tlsx.Files{CA: garbage}, tlsx.ErrEmptyCA},
		{"key file as CA", tlsx.Files{CA: good.Key}, tlsx.ErrEmptyCA},
		{"garbage cert", tlsx.Files{Cert: garbage, Key: good.Key}, nil},
		{"key mismatch", tlsx.Files{Cert: good.Cert, Key: otherKey}, nil},
	}
}

func wantRejected(t *testing.T, tc rejectCase, err error) {
	t.Helper()
	if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
		t.Fatalf("%s: err = %v, want %v", tc.name, err, tc.want)
	}
}

func TestNewReloader_FailsClosed(t *testing.T) {
	t.Parallel()
	good, cases := rejectCases(t)
	for _, tc := range append(cases, rejectCase{"no files", tlsx.Files{}, tlsx.ErrNoFiles}) {
		r, err := tlsx.NewReloader(tc.files, tlsx.Options{})
		if r != nil {
			t.Fatalf("%s: NewReloader returned a reloader next to %v", tc.name, err)
		}
		wantRejected(t, tc, err)
	}

	r, err := tlsx.NewReloader(good, tlsx.Options{})
	if err != nil || r.LastError() != nil {
		t.Fatalf("good files: err=%v", err)
	}
}

func TestLoadClientConfig(t *testing.T) {
	t.Parallel()
	ca := tlsxtest.NewCA(t)
	files := ca.WriteFiles(t, t.TempDir(), ca.Client(t, "client"))

	tests := []struct {
		name      string
		files     tlsx.Files
		wantRoots bool
		wantCerts int
	}{
		{"cert, key and CA", files, true, 1},
		{"CA only", tlsx.Files{CA: files.CA}, true, 0},
		{"pair only keeps system roots", tlsx.Files{Cert: files.Cert, Key: files.Key}, false, 1},
		{"zero files keeps system roots", tlsx.Files{}, false, 0},
	}
	for _, tt := range tests {
		cfg, err := tlsx.LoadClientConfig(tt.files)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if (cfg.RootCAs != nil) != tt.wantRoots || len(cfg.Certificates) != tt.wantCerts || cfg.MinVersion != tls.VersionTLS13 {
			t.Fatalf("%s: RootCAs set=%v certificates=%d MinVersion=%#x", tt.name, cfg.RootCAs != nil, len(cfg.Certificates), cfg.MinVersion)
		}
	}
}

func TestLoadClientConfig_FailsClosed(t *testing.T) {
	t.Parallel()
	_, cases := rejectCases(t)
	for _, tc := range cases {
		cfg, err := tlsx.LoadClientConfig(tc.files)
		if cfg != nil {
			t.Fatalf("%s: LoadClientConfig returned a config next to %v", tc.name, err)
		}
		wantRejected(t, tc, err)
	}
}

func TestLoadClientConfig_MutualTLSReadsOnce(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake()
	ca := tlsxtest.NewCA(t)
	srv := newReloader(t, ca.WriteFiles(t, t.TempDir(), ca.Server(t, host)), clk)
	srvCfg := srv.ServerConfig(tls.RequireAndVerifyClientCert)
	first := ca.Client(t, "client")
	files := ca.WriteFiles(t, t.TempDir(), first)
	cfg, err := tlsx.LoadClientConfig(files)
	if err != nil {
		t.Fatalf("LoadClientConfig: %v", err)
	}

	// Boundary: a rotation after the load does not reach the one-shot config.
	tlsxtest.WritePair(t, files, ca.Client(t, "client"))
	got := handshake(t, srvCfg, cfg)
	if got.err != nil || got.clientSerial.Cmp(first.Serial) != 0 {
		t.Fatalf("err=%v client serial=%v, want %v", got.err, got.clientSerial, first.Serial)
	}

	untrusted, err := tlsx.LoadClientConfig(tlsx.Files{CA: writeFile(t, tlsxtest.NewCA(t).PEM)})
	if err != nil {
		t.Fatalf("LoadClientConfig: %v", err)
	}
	wantFailure(t, handshake(t, srvCfg, untrusted), "server from an untrusted CA")
}

func TestParseClientAuth(t *testing.T) {
	t.Parallel()
	tests := []struct {
		mode   string
		haveCA bool
		want   tls.ClientAuthType
		err    error
	}{
		{"", false, tls.NoClientCert, nil},
		{"", true, tls.RequireAndVerifyClientCert, nil},
		{"none", true, tls.NoClientCert, nil},
		{"request", false, tls.RequestClientCert, nil},
		{"require_any", false, tls.RequireAnyClientCert, nil},
		{" Verify_If_Given ", true, tls.VerifyClientCertIfGiven, nil},
		{"require_and_verify", true, tls.RequireAndVerifyClientCert, nil},
		{"require_and_verify", false, tls.NoClientCert, tlsx.ErrNoClientCA},
		{"verify_if_given", false, tls.NoClientCert, tlsx.ErrNoClientCA},
		{"require", true, tls.NoClientCert, tlsx.ErrClientAuth},
		{"mutual", true, tls.NoClientCert, tlsx.ErrClientAuth},
	}
	for _, tt := range tests {
		got, err := tlsx.ParseClientAuth(tt.mode, tt.haveCA)
		if got != tt.want || !errors.Is(err, tt.err) {
			t.Fatalf("ParseClientAuth(%q, %v) = (%v, %v), want (%v, %v)", tt.mode, tt.haveCA, got, err, tt.want, tt.err)
		}
	}
}

func writeFile(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "file.pem")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
