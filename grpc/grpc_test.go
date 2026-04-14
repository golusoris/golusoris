package grpc_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"google.golang.org/grpc"

	"github.com/golusoris/golusoris/config"
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

// TestModule_StartsAndStops boots the grpc Module via fxtest to cover
// loadConfig, withDefaults, newServer, and newConnFactory.
func TestModule_StartsAndStops(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{})
	require.NoError(t, err)

	app := fxtest.New(t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		ourgrpc.Module,
		fx.Invoke(func(*grpc.Server) {}),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, app.Start(ctx))
	require.NoError(t, app.Stop(ctx))
}
