// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package grpc

import (
	"context"
	"log/slog"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/tlsx"
	"github.com/golusoris/golusoris/core/tlsx/tlsxtest"
)

const holdMethod = "/golusoris.test.Hold/Call"

type holdServer struct {
	started chan struct{}
	release chan struct{}
}

func (h *holdServer) Call(ctx context.Context, _ *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	select {
	case h.started <- struct{}{}:
	default:
	}
	select {
	case <-h.release:
		return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVING}, nil
	case <-ctx.Done():
		return nil, status.FromContextError(ctx.Err()).Err()
	}
}

func holdHandler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	in := new(healthpb.HealthCheckRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	h, ok := srv.(*holdServer)
	if !ok {
		return nil, status.Error(codes.Internal, "hold server not registered")
	}
	if interceptor == nil {
		return h.Call(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: holdMethod}
	return interceptor(ctx, in, info, func(ctx context.Context, req any) (any, error) {
		r, _ := req.(*healthpb.HealthCheckRequest)
		return h.Call(ctx, r)
	})
}

var holdDesc = grpc.ServiceDesc{
	ServiceName: "golusoris.test.Hold",
	HandlerType: (*any)(nil),
	Methods:     []grpc.MethodDesc{{MethodName: "Call", Handler: holdHandler}},
}

func startTLSServer(t *testing.T, cfg Config, clk clock.Clock, hs *holdServer) string {
	t.Helper()
	opts, err := frameworkServerOptions(cfg.withDefaults(), slog.New(slog.DiscardHandler), clk)
	require.NoError(t, err)
	srv := grpc.NewServer(opts...)
	srv.RegisterService(&holdDesc, hs)
	registerHealth(srv, true, nil)
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(srv.Stop)
	return ln.Addr().String()
}

func peerCertSerial(t *testing.T, p peer.Peer) *big.Int {
	t.Helper()
	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	require.True(t, ok, "authInfo must be TLSInfo")
	require.NotEmpty(t, tlsInfo.State.PeerCertificates, "must have peer certificates")
	return tlsInfo.State.PeerCertificates[0].SerialNumber
}

// TestServerTLS_ExistingRPCsFinishAcrossRotation verifies issue #627:
// existing in-flight RPCs finish across a certificate rotation, while new
// connections pick up the rotated certificate.
func TestServerTLS_ExistingRPCsFinishAcrossRotation(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake()
	ca := tlsxtest.NewCA(t)
	cert1 := ca.Server(t, "127.0.0.1")
	dir := t.TempDir()
	srvFiles := ca.WriteFiles(t, dir, cert1)

	hs := &holdServer{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	cfg := Config{
		TLS:      true,
		CertFile: srvFiles.Cert,
		KeyFile:  srvFiles.Key,
	}
	addr := startTLSServer(t, cfg, clk, hs)

	cliTLS, err := tlsx.LoadClientConfig(tlsx.Files{CA: srvFiles.CA})
	require.NoError(t, err)
	cliTLS.ServerName = "127.0.0.1"
	cliCreds := credentials.NewTLS(cliTLS)

	// 1. Establish client connection 1.
	conn1 := dial(t, addr, cliCreds)

	// 2. Open a held RPC on conn1.
	rpcCtx, rpcCancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer rpcCancel()
	var p1 peer.Peer
	rpcDone := make(chan error, 1)
	go func() {
		var resp healthpb.HealthCheckResponse
		rpcDone <- conn1.Invoke(rpcCtx, holdMethod, &healthpb.HealthCheckRequest{}, &resp, grpc.Peer(&p1))
	}()

	waitCtx, waitCancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer waitCancel()
	select {
	case <-hs.started:
	case <-waitCtx.Done():
		t.Fatalf("held RPC failed to start: %v", waitCtx.Err())
	}

	// 3. Rotate server TLS files to cert2.
	cert2 := ca.Server(t, "127.0.0.1")
	require.NotEqual(t, cert1.Serial, cert2.Serial)
	tlsxtest.WritePair(t, srvFiles, cert2)

	// 4. Advance fake clock past MinInterval.
	clk.Advance(tlsx.DefaultMinInterval + time.Second)

	// 5. Make a NEW connection: handshake sees cert2.
	conn2 := dial(t, addr, cliCreds)
	var p2 peer.Peer
	resp, err := healthpb.NewHealthClient(conn2).Check(waitCtx, &healthpb.HealthCheckRequest{}, grpc.Peer(&p2))
	require.NoError(t, err)
	require.Equal(t, healthpb.HealthCheckResponse_SERVING, resp.GetStatus())
	require.Equal(t, cert2.Serial, peerCertSerial(t, p2), "handshake sees cert2")

	// 6. Release the held RPC: it completes OK on the old conn and its peer cert is still cert1.
	close(hs.release)
	select {
	case err := <-rpcDone:
		require.NoError(t, err, "held RPC must complete OK")
	case <-waitCtx.Done():
		t.Fatalf("held RPC timed out completing: %v", waitCtx.Err())
	}
	require.Equal(t, cert1.Serial, peerCertSerial(t, p1), "held RPC on old conn must still see cert1")
}

// TestServerTLS_UntrustedRotationRejectsNewConnKeepsHeldRPC verifies that
// rotating to a certificate the client does not trust causes new connections
// to fail while an existing held RPC still completes.
func TestServerTLS_UntrustedRotationRejectsNewConnKeepsHeldRPC(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake()
	ca := tlsxtest.NewCA(t)
	cert1 := ca.Server(t, "127.0.0.1")
	dir := t.TempDir()
	srvFiles := ca.WriteFiles(t, dir, cert1)

	hs := &holdServer{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	cfg := Config{
		TLS:      true,
		CertFile: srvFiles.Cert,
		KeyFile:  srvFiles.Key,
	}
	addr := startTLSServer(t, cfg, clk, hs)

	cliTLS, err := tlsx.LoadClientConfig(tlsx.Files{CA: srvFiles.CA})
	require.NoError(t, err)
	cliTLS.ServerName = "127.0.0.1"
	cliCreds := credentials.NewTLS(cliTLS)

	// 1. Establish client connection 1.
	conn1 := dial(t, addr, cliCreds)

	// 2. Open a held RPC on conn1.
	rpcCtx, rpcCancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer rpcCancel()
	var p1 peer.Peer
	rpcDone := make(chan error, 1)
	go func() {
		var resp healthpb.HealthCheckResponse
		rpcDone <- conn1.Invoke(rpcCtx, holdMethod, &healthpb.HealthCheckRequest{}, &resp, grpc.Peer(&p1))
	}()

	waitCtx, waitCancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer waitCancel()
	select {
	case <-hs.started:
	case <-waitCtx.Done():
		t.Fatalf("held RPC failed to start: %v", waitCtx.Err())
	}

	// 3. Rotate server files to a cert issued by an untrusted CA.
	untrustedCA := tlsxtest.NewCA(t)
	certUntrusted := untrustedCA.Server(t, "127.0.0.1")
	tlsxtest.WritePair(t, srvFiles, certUntrusted)

	// 4. Advance fake clock past MinInterval.
	clk.Advance(tlsx.DefaultMinInterval + time.Second)

	// 5. Make a NEW connection with client creds (which only trust ca): handshake fails!
	conn2 := dial(t, addr, cliCreds)
	_, err = healthpb.NewHealthClient(conn2).Check(waitCtx, &healthpb.HealthCheckRequest{})
	require.Error(t, err, "new conn with untrusted cert must fail handshake")
	require.Equal(t, codes.Unavailable, status.Code(err), "err = %v", err)

	// 6. Release the held RPC: it still completes OK on the old conn and its peer cert is cert1.
	close(hs.release)
	select {
	case err := <-rpcDone:
		require.NoError(t, err, "held RPC must still complete OK")
	case <-waitCtx.Done():
		t.Fatalf("held RPC timed out completing: %v", waitCtx.Err())
	}
	require.Equal(t, cert1.Serial, peerCertSerial(t, p1), "held RPC on old conn must still see cert1")
}
