// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package idempotency_test

import (
	"context"
	"net"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	"github.com/golusoris/golusoris/idempotency"
)

// countingHealth answers Check with a fresh status per execution.
type countingHealth struct {
	grpc_health_v1.UnimplementedHealthServer

	calls atomic.Int32
}

func (h *countingHealth) Check(context.Context, *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	if h.calls.Add(1) == 1 {
		return &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING}, nil
	}
	return &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_NOT_SERVING}, nil
}

func (h *countingHealth) Watch(*grpc_health_v1.HealthCheckRequest, grpc.ServerStreamingServer[grpc_health_v1.HealthCheckResponse]) error {
	h.calls.Add(1)
	return nil
}

// TestInterceptors_OverRealServer drives both interceptors through a real
// gRPC server: metadata arrives, replay survives wire encoding, keyed streams fail.
func TestInterceptors_OverRealServer(t *testing.T) {
	t.Parallel()
	listener := bufconn.Listen(1 << 20)
	health := &countingHealth{}
	server := grpc.NewServer(
		grpc.ChainUnaryInterceptor(idempotency.UnaryServerInterceptor(idempotency.NewMemoryStore(), idempotency.GRPCOptions{})),
		grpc.ChainStreamInterceptor(idempotency.StreamServerInterceptor(idempotency.GRPCOptions{})),
	)
	grpc_health_v1.RegisterHealthServer(server, health)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := grpc_health_v1.NewHealthClient(conn)
	ctx := metadata.AppendToOutgoingContext(t.Context(), idempotency.DefaultGRPCMetadata, "check-1")

	first, err := client.Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: "payments"})
	require.NoError(t, err)
	second, err := client.Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: "payments"})
	require.NoError(t, err)
	require.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, first.GetStatus())
	require.Equal(t, first.GetStatus(), second.GetStatus(), "replay returns the first response")
	require.EqualValues(t, 1, health.calls.Load())

	_, err = client.Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: "refunds"})
	requireCode(t, err, codes.FailedPrecondition)

	stream, err := client.Watch(ctx, &grpc_health_v1.HealthCheckRequest{Service: "payments"})
	require.NoError(t, err)
	_, err = stream.Recv()
	requireCode(t, err, codes.InvalidArgument)
	require.EqualValues(t, 1, health.calls.Load(), "keyed stream never reached the handler")
}
