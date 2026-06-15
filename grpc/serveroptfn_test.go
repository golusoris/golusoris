package grpc_test

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"google.golang.org/grpc"

	"github.com/golusoris/golusoris/config"
	ourgrpc "github.com/golusoris/golusoris/grpc"
)

// authMW stands in for a graph-constructed singleton an interceptor depends on.
type authMW struct{ used *atomic.Bool }

// TestProvideServerOptionFn is the #269 acceptance: a ServerOption built from an
// fx-constructed dependency (not a concrete value) is wired into the server.
func TestProvideServerOptionFn(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{})
	require.NoError(t, err)

	used := &atomic.Bool{}
	app := fxtest.New(
		t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		fx.Provide(func() *authMW { return &authMW{used: used} }),
		ourgrpc.Module,
		fx.Decorate(func(c ourgrpc.Config) ourgrpc.Config { c.Listen = ":0"; return c }),
		// The ServerOption is constructed from the fx-built *authMW.
		ourgrpc.ProvideServerOptionFn(func(m *authMW) grpc.ServerOption {
			m.used.Store(true)
			return grpc.ChainUnaryInterceptor(func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
				return h(ctx, req)
			})
		}),
		fx.Invoke(func(*grpc.Server) {}),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, app.Start(ctx))
	require.NoError(t, app.Stop(ctx))
	require.True(t, used.Load(), "the ServerOption constructor was not built from its fx dependency")
}
