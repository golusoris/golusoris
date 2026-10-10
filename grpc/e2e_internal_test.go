// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package grpc

import (
	"context"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/golusoris/golusoris/core/tlsx"
	"github.com/golusoris/golusoris/core/tlsx/tlsxtest"
)

const sleepMethod = "/golusoris.test.Slow/Sleep"

// slowService answers Sleep after the duration named in the request's Service
// field; it reuses the health messages so the test needs no generated code.
type slowService interface {
	Sleep(ctx context.Context, in *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error)
}

type sleeper struct{}

func (sleeper) Sleep(ctx context.Context, in *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	d, err := time.ParseDuration(in.GetService())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}, nil
	case <-ctx.Done():
		return nil, status.FromContextError(ctx.Err()).Err()
	}
}

func sleepHandler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	in := new(healthpb.HealthCheckRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	svc, ok := srv.(slowService)
	if !ok {
		return nil, status.Error(codes.Internal, "slow service not registered")
	}
	if interceptor == nil {
		return svc.Sleep(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: sleepMethod}
	return interceptor(ctx, in, info, func(ctx context.Context, req any) (any, error) {
		r, _ := req.(*healthpb.HealthCheckRequest)
		return svc.Sleep(ctx, r)
	})
}

var slowDesc = grpc.ServiceDesc{
	ServiceName: "golusoris.test.Slow",
	HandlerType: (*slowService)(nil),
	Methods:     []grpc.MethodDesc{{MethodName: "Sleep", Handler: sleepHandler}},
}

// startServer serves the framework options for cfg plus the Slow service on loopback.
func startServer(t *testing.T, cfg Config) string {
	t.Helper()
	return startServerWith(t, cfg, nil)
}

// startServerWith also lets register add test services before serving.
func startServerWith(t *testing.T, cfg Config, register func(*grpc.Server)) string {
	t.Helper()
	opts, err := frameworkServerOptions(cfg.withDefaults(), slog.New(slog.DiscardHandler), nil)
	require.NoError(t, err)
	srv := grpc.NewServer(opts...)
	srv.RegisterService(&slowDesc, sleeper{})
	registerHealth(srv, true, nil)
	if register != nil {
		register(srv)
	}
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(srv.Stop)
	return ln.Addr().String()
}

// mtlsFixture is a server config with mTLS plus client credentials from one CA.
type mtlsFixture struct {
	server Config
	client credentials.TransportCredentials
	ca     *tlsxtest.CA
}

func newMTLS(t *testing.T) mtlsFixture {
	t.Helper()
	ca := tlsxtest.NewCA(t)
	srvFiles := ca.WriteFiles(t, t.TempDir(), ca.Server(t, "127.0.0.1"))
	cliReloader, err := tlsx.NewReloader(ca.WriteFiles(t, t.TempDir(), ca.Client(t, "client")), tlsx.Options{})
	require.NoError(t, err)
	return mtlsFixture{
		server: Config{
			TLS: true, CertFile: srvFiles.Cert, KeyFile: srvFiles.Key, CAFile: srvFiles.CA,
			ClientAuth: "require_and_verify",
		},
		client: credentials.NewTLS(cliReloader.ClientConfig("")),
		ca:     ca,
	}
}

func dial(t *testing.T, addr string, creds credentials.TransportCredentials) *grpc.ClientConn {
	t.Helper()
	conn, err := NewConnFactory().Dial(context.Background(), addr, grpc.WithTransportCredentials(creds))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func sleepRPC(conn *grpc.ClientConn, d time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return conn.Invoke(ctx, sleepMethod, &healthpb.HealthCheckRequest{Service: d.String()}, &healthpb.HealthCheckResponse{})
}

// TestLongRPC_ConnectionAge is the #589 acceptance over mTLS: an RPC running
// well past MaxConnectionAge completes when age is disabled or the grace is
// unlimited, and is cut with UNAVAILABLE when age and grace are short.
func TestLongRPC_ConnectionAge(t *testing.T) {
	t.Parallel()
	const age, rpc = 300 * time.Millisecond, 1500 * time.Millisecond
	tests := []struct {
		name      string
		keepalive KeepaliveConfig
		wantCode  codes.Code
	}{
		{"age disabled", KeepaliveConfig{MaxConnectionAge: Infinite, MaxConnectionAgeGrace: 200 * time.Millisecond}, codes.OK},
		{"unlimited grace", KeepaliveConfig{MaxConnectionAge: age, MaxConnectionAgeGrace: Infinite}, codes.OK},
		{"short age and grace", KeepaliveConfig{MaxConnectionAge: age, MaxConnectionAgeGrace: 200 * time.Millisecond}, codes.Unavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newMTLS(t)
			cfg := f.server
			cfg.Keepalive = tt.keepalive
			conn := dial(t, startServer(t, cfg), f.client)
			err := sleepRPC(conn, rpc)
			require.Equal(t, tt.wantCode, status.Code(err), "err = %v", err)
		})
	}
}

func TestMTLS_ServerRejectsClients(t *testing.T) {
	t.Parallel()
	f := newMTLS(t)
	addr := startServer(t, f.server)

	// Positive: the CA-issued client reaches the health service.
	resp, err := healthpb.NewHealthClient(dial(t, addr, f.client)).Check(context.Background(), &healthpb.HealthCheckRequest{})
	require.NoError(t, err)
	require.Equal(t, healthpb.HealthCheckResponse_SERVING, resp.GetStatus())

	// Negative: a client that trusts the server but holds no certificate.
	caOnly, err := tlsx.NewReloader(tlsx.Files{CA: f.ca.WriteFiles(t, t.TempDir(), f.ca.Client(t, "x")).CA}, tlsx.Options{})
	require.NoError(t, err)
	err = sleepRPC(dial(t, addr, credentials.NewTLS(caOnly.ClientConfig(""))), time.Millisecond)
	require.Equal(t, codes.Unavailable, status.Code(err), "err = %v", err)

	// Negative: a plaintext client.
	plain, err := NewConnFactory().Dial(context.Background(), addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = plain.Close() })
	require.Equal(t, codes.Unavailable, status.Code(sleepRPC(plain, time.Millisecond)))
}

func TestServerTLS_ConfigErrors(t *testing.T) {
	t.Parallel()
	f := newMTLS(t)
	logger := slog.New(slog.DiscardHandler)
	tests := []struct {
		name   string
		mutate func(*Config)
		want   error
	}{
		{"no cert", func(c *Config) { c.CertFile = "" }, errTLSPair},
		{"no key", func(c *Config) { c.KeyFile = "" }, errTLSPair},
		{"unknown client auth", func(c *Config) { c.ClientAuth = "require" }, tlsx.ErrClientAuth},
		{"verify without CA", func(c *Config) { c.CAFile = "" }, tlsx.ErrNoClientCA},
		{"missing CA file", func(c *Config) { c.CAFile += ".missing" }, nil},
	}
	for _, tt := range tests {
		cfg := f.server
		tt.mutate(&cfg)
		_, err := frameworkServerOptions(cfg.withDefaults(), logger, nil)
		require.Error(t, err, tt.name)
		if tt.want != nil {
			require.ErrorIs(t, err, tt.want, tt.name)
		}
	}

	// Boundary: TLS off ignores the TLS fields entirely, as before.
	cfg := f.server
	cfg.TLS, cfg.CertFile = false, ""
	_, err := frameworkServerOptions(cfg.withDefaults(), logger, nil)
	require.NoError(t, err)
}

// TestServerHook_StopClosesLateServeListener pins #724: a Serve that starts
// after the stop closes its listener on its own goroutine, and OnStop must not
// return before it has.
func TestServerHook_StopClosesLateServeListener(t *testing.T) {
	t.Parallel()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	srv := grpc.NewServer()
	srv.Stop() // forces grpc-go's "Serve called after Stop" path
	hook := serverHook(srv, nil, Config{Listen: addr}, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.NoError(t, hook.OnStart(ctx))
	require.NoError(t, hook.OnStop(ctx))

	again, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	require.NoError(t, err, "listener must be closed once OnStop returns")
	require.NoError(t, again.Close())
}
