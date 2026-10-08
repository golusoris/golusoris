// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package server_test

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/httpx/server"
	"github.com/golusoris/golusoris/k8s/health"
	"github.com/golusoris/golusoris/observability/statuspage"
)

const drainDelay = 5 * time.Second

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

// newTestClient uses a private transport: httptest.Server.Close resets http.DefaultTransport (#701).
func newTestClient(t *testing.T) *http.Client {
	t.Helper()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 10 * time.Second}
}

func drainConfig(t *testing.T, addr string) *config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := fmt.Sprintf("http:\n  addr: %q\nhealth:\n  drain:\n    delay: %s\n", addr, drainDelay)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	cfg, err := config.New(config.Options{EnvPrefix: "GOLUSORIS_SERVER_DRAIN_TEST_", Files: []string{path}})
	require.NoError(t, err)
	return cfg
}

func get(ctx context.Context, client *http.Client, url string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("get %s: %w", url, err)
	}
	if err := resp.Body.Close(); err != nil {
		return 0, fmt.Errorf("close body: %w", err)
	}
	return resp.StatusCode, nil
}

// TestModuleServesReadyz503DuringDrain proves the server keeps serving with
// /readyz failing for the whole drain window, and shuts down only after it.
func TestModuleServesReadyz503DuringDrain(t *testing.T) {
	t.Parallel()
	addr := freeAddr(t)
	fc := clock.NewFake()
	app := fx.New(
		fx.NopLogger,
		fx.Supply(drainConfig(t, addr)),
		fx.Provide(func() clock.Clock { return fc }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		fx.Provide(statuspage.NewRegistry),
		fx.Provide(func(reg *statuspage.Registry) http.Handler {
			mux := http.NewServeMux()
			health.MountMux(mux, reg)
			return mux
		}),
		server.Module,
		health.Module,
		fx.Invoke(func(*http.Server) {}),
	)
	require.NoError(t, app.Err())
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, app.Start(ctx))
	base := "http://" + addr
	client := newTestClient(t)

	code, err := get(ctx, client, base+"/readyz")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, code)

	stopped := make(chan error, 1)
	go func() { stopped <- app.Stop(ctx) }()
	require.NoError(t, fc.BlockUntilContext(ctx, 1))

	code, err = get(ctx, client, base+"/readyz")
	require.NoError(t, err, "server must still serve during the drain window")
	require.Equal(t, http.StatusServiceUnavailable, code)
	code, err = get(ctx, client, base+"/livez")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, code, "liveness must stay up while draining")

	fc.Advance(drainDelay)
	require.NoError(t, <-stopped)
	_, err = get(ctx, client, base+"/livez")
	require.Error(t, err, "server must be shut down after the drain window")
}
