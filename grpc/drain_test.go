// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package grpc_test

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"google.golang.org/grpc"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	ourgrpc "github.com/golusoris/golusoris/grpc"
	"github.com/golusoris/golusoris/k8s/health"
	"github.com/golusoris/golusoris/observability/statuspage"
)

const drainDelay = 5 * time.Second

func dialable(ctx context.Context, addr string) error {
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	if err := conn.Close(); err != nil {
		return fmt.Errorf("close %s: %w", addr, err)
	}
	return nil
}

// TestModuleGracefulStopWaitsForDrain proves the gRPC listener stays open
// for the whole readiness drain window and closes only after it.
func TestModuleGracefulStopWaitsForDrain(t *testing.T) {
	t.Parallel()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	path := filepath.Join(t.TempDir(), "config.yaml")
	body := fmt.Sprintf("grpc:\n  listen: %q\nhealth:\n  drain:\n    delay: %s\n", addr, drainDelay)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	cfg, err := config.New(config.Options{EnvPrefix: "GOLUSORIS_GRPC_DRAIN_TEST_", Files: []string{path}})
	require.NoError(t, err)

	fc := clock.NewFake()
	var reg *statuspage.Registry
	app := fx.New(
		fx.NopLogger,
		fx.Supply(cfg),
		fx.Provide(func() clock.Clock { return fc }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		fx.Provide(statuspage.NewRegistry),
		ourgrpc.Module,
		health.Module,
		fx.Invoke(func(*grpc.Server) {}),
		fx.Populate(&reg),
	)
	require.NoError(t, app.Err())
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, app.Start(ctx))
	require.Eventually(t, func() bool { return dialable(ctx, addr) == nil }, 5*time.Second, 10*time.Millisecond)

	stopped := make(chan error, 1)
	go func() { stopped <- app.Stop(ctx) }()
	require.NoError(t, fc.BlockUntilContext(ctx, 1))

	require.NoError(t, dialable(ctx, addr), "gRPC must keep serving during the drain window")
	for _, res := range reg.RunTagged(ctx, health.TagReadiness) {
		require.Equal(t, statuspage.StatusDown, res.Status, res.Name)
	}

	fc.Advance(drainDelay)
	require.NoError(t, <-stopped)
	require.Error(t, dialable(ctx, addr), "listener must be closed after the drain window")
}
