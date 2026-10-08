// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package server

import (
	"context"
	"crypto/tls"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/tlsx"
	"github.com/golusoris/golusoris/core/tlsx/tlsxtest"
)

var discard = slog.New(slog.DiscardHandler)

func TestResolveTLS_KeepsAutotlsPath(t *testing.T) {
	t.Parallel()
	provided := &tls.Config{MinVersion: tls.VersionTLS12}
	got, err := resolveTLS(TLSOptions{}, provided, discard, nil)
	require.NoError(t, err)
	require.Same(t, provided, got)

	got, err = resolveTLS(TLSOptions{}, nil, discard, nil)
	require.NoError(t, err)
	require.Nil(t, got, "no TLS options and no provider stays plaintext")
}

func TestResolveTLS_Errors(t *testing.T) {
	t.Parallel()
	ca := tlsxtest.NewCA(t)
	files := ca.WriteFiles(t, t.TempDir(), ca.Server(t, "127.0.0.1"))
	good := TLSOptions{Cert: files.Cert, Key: files.Key, CA: files.CA}
	tests := []struct {
		name     string
		opts     TLSOptions
		provided *tls.Config
		want     error
	}{
		{"autotls conflict", good, &tls.Config{MinVersion: tls.VersionTLS13}, errTLSConflict},
		{"CA only", TLSOptions{CA: files.CA}, nil, errTLSPair},
		{"cert without key", TLSOptions{Cert: files.Cert}, nil, errTLSPair},
		{"unknown clientauth", TLSOptions{Cert: files.Cert, Key: files.Key, ClientAuth: "require"}, nil, tlsx.ErrClientAuth},
		{"verify without CA", TLSOptions{Cert: files.Cert, Key: files.Key, ClientAuth: "verify_if_given"}, nil, tlsx.ErrNoClientCA},
		{"missing key file", TLSOptions{Cert: files.Cert, Key: files.Key + ".missing"}, nil, nil},
	}
	for _, tt := range tests {
		got, err := resolveTLS(tt.opts, tt.provided, discard, nil)
		require.Error(t, err, tt.name)
		require.Nil(t, got, tt.name)
		if tt.want != nil {
			require.ErrorIs(t, err, tt.want, tt.name)
		}
	}
}

// serveTLS serves 200 over a TLS listener using cfg and returns the address.
func serveTLS(t *testing.T, cfg *tls.Config) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := New(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }), Options{})
	srv.TLSConfig = cfg
	go func() { _ = srv.Serve(tls.NewListener(ln, cfg)) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String()
}

// get performs one GET with a fresh transport and returns the response facts.
func get(t *testing.T, addr string, cfg *tls.Config) (proto int, serial *big.Int, err error) {
	t.Helper()
	tr := &http.Transport{TLSClientConfig: cfg, ForceAttemptHTTP2: true}
	defer tr.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+addr+"/", http.NoBody)
	require.NoError(t, err)
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	return resp.ProtoMajor, resp.TLS.PeerCertificates[0].SerialNumber, nil
}

func TestResolveTLS_ServesHTTP2AndRotates(t *testing.T) {
	t.Parallel()
	ca := tlsxtest.NewCA(t)
	first := ca.Server(t, "127.0.0.1")
	files := ca.WriteFiles(t, t.TempDir(), first)
	clk := clock.NewFake()
	cfg, err := resolveTLS(TLSOptions{Cert: files.Cert, Key: files.Key}, nil, discard, clk)
	require.NoError(t, err)
	require.Equal(t, uint16(tls.VersionTLS13), cfg.MinVersion)
	addr := serveTLS(t, cfg)

	cli, err := tlsx.NewReloader(tlsx.Files{CA: files.CA}, tlsx.Options{})
	require.NoError(t, err)
	proto, serial, err := get(t, addr, cli.ClientConfig(""))
	require.NoError(t, err)
	require.Equal(t, 2, proto, "h2 negotiated")
	require.Equal(t, 0, first.Serial.Cmp(serial))

	second := ca.Server(t, "127.0.0.1")
	tlsxtest.WritePair(t, files, second)
	clk.Advance(tlsx.DefaultMinInterval)
	_, serial, err = get(t, addr, cli.ClientConfig(""))
	require.NoError(t, err)
	require.Equal(t, 0, second.Serial.Cmp(serial), "rotated certificate served")
}

func TestResolveTLS_MutualTLS(t *testing.T) {
	t.Parallel()
	ca := tlsxtest.NewCA(t)
	srvFiles := ca.WriteFiles(t, t.TempDir(), ca.Server(t, "127.0.0.1"))
	// Empty clientauth with a CA defaults to require_and_verify.
	cfg, err := resolveTLS(TLSOptions{Cert: srvFiles.Cert, Key: srvFiles.Key, CA: srvFiles.CA}, nil, discard, nil)
	require.NoError(t, err)
	require.Equal(t, tls.RequireAndVerifyClientCert, cfg.ClientAuth)
	addr := serveTLS(t, cfg)

	withCert, err := tlsx.NewReloader(ca.WriteFiles(t, t.TempDir(), ca.Client(t, "client")), tlsx.Options{})
	require.NoError(t, err)
	_, _, err = get(t, addr, withCert.ClientConfig(""))
	require.NoError(t, err)

	caOnly, err := tlsx.NewReloader(tlsx.Files{CA: srvFiles.CA}, tlsx.Options{})
	require.NoError(t, err)
	_, _, err = get(t, addr, caOnly.ClientConfig(""))
	require.Error(t, err, "client without certificate is refused")
}
