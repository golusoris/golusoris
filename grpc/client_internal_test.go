// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package grpc

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/tlsx"
	"github.com/golusoris/golusoris/core/tlsx/tlsxtest"
)

const flakyMethod = "/golusoris.test.Flaky/Call"

// flaky fails its first failFirst calls with UNAVAILABLE and counts every call.
type flaky struct {
	failFirst int32
	calls     atomic.Int32
}

type flakyService interface{ call() error }

func (f *flaky) call() error {
	if f.calls.Add(1) <= f.failFirst {
		return status.Error(codes.Unavailable, "warming up")
	}
	return nil
}

var flakyDesc = grpc.ServiceDesc{
	ServiceName: "golusoris.test.Flaky",
	HandlerType: (*flakyService)(nil),
	Methods: []grpc.MethodDesc{{MethodName: "Call", Handler: func(srv any, _ context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
		if err := dec(new(healthpb.HealthCheckRequest)); err != nil {
			return nil, err
		}
		svc, _ := srv.(flakyService)
		if err := svc.call(); err != nil {
			return nil, err
		}
		return &healthpb.HealthCheckResponse{}, nil
	}}},
}

func callFlaky(t *testing.T, f *ConnFactory, addr string) error {
	t.Helper()
	conn, err := f.Dial(context.Background(), addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return conn.Invoke(ctx, flakyMethod, &healthpb.HealthCheckRequest{}, &healthpb.HealthCheckResponse{})
}

func TestClientRetry_Unavailable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		attempts  int
		wantCode  codes.Code
		wantCalls int32
	}{
		{"retries disabled", 0, codes.Unavailable, 1},
		{"boundary: one attempt", 1, codes.Unavailable, 1},
		{"two attempts are too few", 2, codes.Unavailable, 2},
		{"three attempts succeed", 3, codes.OK, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc := &flaky{failFirst: 2}
			addr := startServerWith(t, Config{}, func(s *grpc.Server) { s.RegisterService(&flakyDesc, svc) })
			cfg := ClientConfig{Retry: RetryConfig{MaxAttempts: tt.attempts, InitialBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond}}
			f, err := NewConnFactoryWithConfig(cfg, nil)
			require.NoError(t, err)
			require.Equal(t, tt.wantCode, status.Code(callFlaky(t, f, addr)))
			require.Equal(t, tt.wantCalls, svc.calls.Load())
		})
	}
}

func TestRetryServiceConfig_Shape(t *testing.T) {
	t.Parallel()
	sc, err := retryServiceConfig(RetryConfig{MaxAttempts: 4}.withDefaults())
	require.NoError(t, err)
	var got map[string][]map[string]any
	require.NoError(t, json.Unmarshal([]byte(sc), &got))
	methods := got["methodConfig"]
	require.Len(t, methods, 1)
	require.Equal(t, []any{map[string]any{}}, methods[0]["name"], "applies to every method")
	policy, ok := methods[0]["retryPolicy"].(map[string]any)
	require.True(t, ok)
	require.InDelta(t, 4, policy["maxAttempts"], 0)
	require.Equal(t, "0.100000000s", policy["initialBackoff"])
	require.Equal(t, "1.000000000s", policy["maxBackoff"])
	require.InDelta(t, 2, policy["backoffMultiplier"], 0)
	require.Equal(t, []any{"UNAVAILABLE"}, policy["retryableStatusCodes"])

	// grpc-go accepts the rendered config.
	conn, err := grpc.NewClient("passthrough:///unused", grpc.WithDefaultServiceConfig(sc), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	require.NoError(t, conn.Close())
}

func TestProtoDuration(t *testing.T) {
	t.Parallel()
	require.Equal(t, "0.000000000s", protoDuration(0))
	require.Equal(t, "0.000000001s", protoDuration(time.Nanosecond))
	require.Equal(t, "1.500000000s", protoDuration(1500*time.Millisecond))
	require.Equal(t, "120.000000000s", protoDuration(2*time.Minute))
}

func TestClientConfig_Validation(t *testing.T) {
	t.Parallel()
	ca := tlsxtest.NewCA(t)
	files := ca.WriteFiles(t, t.TempDir(), ca.Client(t, "client"))
	tests := []struct {
		name string
		cfg  ClientConfig
		want error
	}{
		{"files without tls", ClientConfig{CAFile: files.CA}, errClientFilesWithoutTLS},
		{"negative keepalive", ClientConfig{Keepalive: ClientKeepaliveConfig{Time: -time.Nanosecond}}, errNegativeClientConfig},
		{"negative backoff", ClientConfig{Retry: RetryConfig{MaxAttempts: 3, InitialBackoff: -1}}, errNegativeClientConfig},
		{"negative multiplier", ClientConfig{Retry: RetryConfig{BackoffMultiplier: -0.5}}, errNegativeClientConfig},
		{"partial pair", ClientConfig{TLS: true, CertFile: files.Cert}, tlsx.ErrPartialPair},
		{"missing CA", ClientConfig{TLS: true, CAFile: files.CA + ".missing"}, nil},
	}
	for _, tt := range tests {
		f, err := NewConnFactoryWithConfig(tt.cfg, nil)
		require.Error(t, err, tt.name)
		require.Nil(t, f, tt.name)
		if tt.want != nil {
			require.ErrorIs(t, err, tt.want, tt.name)
		}
	}
}

func TestClientConfig_DialOptions(t *testing.T) {
	t.Parallel()
	count := func(cfg ClientConfig) int {
		f, err := NewConnFactoryWithConfig(cfg, nil)
		require.NoError(t, err)
		return len(f.dialOpts)
	}
	base := count(ClientConfig{})
	require.Equal(t, len(NewConnFactory().dialOpts), base, "zero config matches NewConnFactory")
	require.Equal(t, base, count(ClientConfig{Keepalive: ClientKeepaliveConfig{Timeout: time.Second}}), "zero time: no pings")
	require.Equal(t, base+1, count(ClientConfig{Keepalive: ClientKeepaliveConfig{Time: time.Nanosecond}}))
	require.Equal(t, base+1, count(ClientConfig{ServerName: "svc.internal"}))
	require.Equal(t, base, count(ClientConfig{Retry: RetryConfig{MaxAttempts: 1}}))
	require.Equal(t, base+1, count(ClientConfig{Retry: RetryConfig{MaxAttempts: 2}}))
}

// clientMTLS returns a client config presenting a CA-issued cert, plus a matching mTLS server config.
func clientMTLS(t *testing.T, serverHosts ...string) (ClientConfig, Config) {
	t.Helper()
	ca := tlsxtest.NewCA(t)
	srv := ca.WriteFiles(t, t.TempDir(), ca.Server(t, serverHosts...))
	cli := ca.WriteFiles(t, t.TempDir(), ca.Client(t, "client"))
	return ClientConfig{TLS: true, CertFile: cli.Cert, KeyFile: cli.Key, CAFile: cli.CA},
		Config{TLS: true, CertFile: srv.Cert, KeyFile: srv.Key, CAFile: srv.CA}
}

func checkHealth(t *testing.T, f *ConnFactory, addr string) error {
	t.Helper()
	conn, err := f.Dial(context.Background(), addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	return err
}

func TestClientTLS_FromFiles(t *testing.T) {
	t.Parallel()
	cliCfg, srvCfg := clientMTLS(t, "127.0.0.1")
	addr := startServer(t, srvCfg)

	f, err := NewConnFactoryWithConfig(cliCfg, nil)
	require.NoError(t, err)
	require.NoError(t, checkHealth(t, f, addr))

	// TLS without files verifies against the system roots, which reject the test CA.
	sysRoots, err := NewConnFactoryWithConfig(ClientConfig{TLS: true}, nil)
	require.NoError(t, err)
	require.Equal(t, codes.Unavailable, status.Code(checkHealth(t, sysRoots, addr)))

	// Plaintext client against the TLS server.
	require.Equal(t, codes.Unavailable, status.Code(checkHealth(t, NewConnFactory(), addr)))
}

func TestClientTLS_ServerName(t *testing.T) {
	t.Parallel()
	cliCfg, srvCfg := clientMTLS(t, "svc.internal")
	addr := startServer(t, srvCfg)

	f, err := NewConnFactoryWithConfig(cliCfg, nil)
	require.NoError(t, err)
	require.Equal(t, codes.Unavailable, status.Code(checkHealth(t, f, addr)), "IP target does not match the DNS-only cert")

	cliCfg.ServerName = "svc.internal"
	named, err := NewConnFactoryWithConfig(cliCfg, nil)
	require.NoError(t, err)
	require.NoError(t, checkHealth(t, named, addr))
}

// TestClientTLS_CARotation proves new connections pick up a rotated CA bundle.
func TestClientTLS_CARotation(t *testing.T) {
	t.Parallel()
	cliCfg, srvCfg := clientMTLS(t, "127.0.0.1")
	addr := startServer(t, srvCfg)

	trustedCA := tlsxtest.NewCA(t) // client initially trusts an unrelated CA
	realCA := cliCfg.CAFile
	cliCfg.CAFile = realCA + ".client"
	trustedCA.WriteCA(t, cliCfg.CAFile)

	clk := clock.NewFake()
	f, err := newClientFactory(cliCfg, slog.New(slog.DiscardHandler), clk)
	require.NoError(t, err)
	require.Equal(t, codes.Unavailable, status.Code(checkHealth(t, f, addr)))

	pem, err := os.ReadFile(filepath.Clean(realCA))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cliCfg.CAFile, pem, 0o600))
	clk.Advance(tlsx.DefaultMinInterval)
	require.NoError(t, checkHealth(t, f, addr))
}
