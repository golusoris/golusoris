package grpc_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	ourgrpc "github.com/golusoris/golusoris/grpc"
)

func TestDefaultConfig(t *testing.T) {
	t.Parallel()
	d := ourgrpc.DefaultConfig()
	require.Equal(t, ":9090", d.Listen)
	require.Equal(t, 4<<20, d.MaxRecvSize)
	require.Equal(t, 4<<20, d.MaxSendSize)
}

func TestConnFactory_Dial_validTarget(t *testing.T) {
	t.Parallel()
	f := ourgrpc.NewConnFactory()
	// grpc.NewClient with insecure creds should succeed for a valid address
	// (connection is lazy — it doesn't dial until the first RPC).
	conn, err := f.Dial(context.Background(), "localhost:50051")
	require.NoError(t, err)
	require.NotNil(t, conn)
	require.NoError(t, conn.Close())
}
