package redis

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	redistest "github.com/golusoris/golusoris/testutil/redis"
)

// TestNewClient_RealRedis connects newClient to a real Redis container and
// round-trips a SET/GET. newClient carries no unit coverage because it dials a
// live server on construction.
func TestNewClient_RealRedis(t *testing.T) {
	t.Parallel()
	addr := redistest.Addr(t)

	logger := slog.New(slog.DiscardHandler)
	client, err := newClient(Options{Addr: addr}, logger)
	require.NoError(t, err)
	t.Cleanup(client.Close)

	ctx := context.Background()
	set := client.B().Set().Key("k").Value("v").Build()
	require.NoError(t, client.Do(ctx, set).Error())

	get := client.B().Get().Key("k").Build()
	got, err := client.Do(ctx, get).ToString()
	require.NoError(t, err)
	require.Equal(t, "v", got)
}

// TestNewClient_TrimsAddrWhitespace verifies the comma-split + TrimSpace path
// in newClient by passing a padded single address to a real Redis.
func TestNewClient_TrimsAddrWhitespace(t *testing.T) {
	t.Parallel()
	addr := redistest.Addr(t)

	logger := slog.New(slog.DiscardHandler)
	client, err := newClient(Options{Addr: "  " + addr + "  "}, logger)
	require.NoError(t, err)
	t.Cleanup(client.Close)

	require.NoError(t, client.Do(context.Background(), client.B().Ping().Build()).Error())
}

// TestNewClient_BadAddr asserts newClient surfaces a wrapped error when the
// target is unreachable.
func TestNewClient_BadAddr(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	_, err := newClient(Options{Addr: "127.0.0.1:1"}, logger)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cache/redis: new client")
}
