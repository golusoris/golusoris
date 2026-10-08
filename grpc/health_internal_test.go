// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package grpc

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpchealth "google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/k8s/health"
	"github.com/golusoris/golusoris/observability/statuspage"
)

const healthService = "grpc.health.v1.Health"

// readinessRegistry returns a registry whose single readiness check follows ready.
func readinessRegistry(ready *atomic.Bool) *statuspage.Registry {
	reg := statuspage.NewRegistry(clock.NewFake())
	reg.Register(statuspage.Check{
		Name: "dep",
		Tags: []string{health.TagReadiness},
		Fn: func(context.Context) error {
			if ready.Load() {
				return nil
			}
			return errors.New("dependency down")
		},
	})
	return reg
}

func checkStatus(t *testing.T, h *readinessHealth, service string) (healthpb.HealthCheckResponse_ServingStatus, error) {
	t.Helper()
	resp, err := h.Check(context.Background(), &healthpb.HealthCheckRequest{Service: service})
	return resp.GetStatus(), err
}

func TestHealth_FollowsReadiness(t *testing.T) {
	t.Parallel()
	ready := &atomic.Bool{}
	h := &readinessHealth{Server: grpchealth.NewServer(), reg: readinessRegistry(ready)}

	got, err := checkStatus(t, h, "")
	require.NoError(t, err)
	require.Equal(t, healthpb.HealthCheckResponse_NOT_SERVING, got)

	ready.Store(true)
	got, err = checkStatus(t, h, "")
	require.NoError(t, err)
	require.Equal(t, healthpb.HealthCheckResponse_SERVING, got)

	// Shutdown latches NOT_SERVING even while readiness passes.
	h.Shutdown()
	got, err = checkStatus(t, h, "")
	require.NoError(t, err)
	require.Equal(t, healthpb.HealthCheckResponse_NOT_SERVING, got)
}

func TestHealth_WithoutRegistryServes(t *testing.T) {
	t.Parallel()
	h := &readinessHealth{Server: grpchealth.NewServer()}
	got, err := checkStatus(t, h, "")
	require.NoError(t, err)
	require.Equal(t, healthpb.HealthCheckResponse_SERVING, got)

	// Unknown services answer NOT_FOUND unwrapped.
	_, err = checkStatus(t, h, "nope.Service")
	require.Equal(t, codes.NotFound, status.Code(err))
}

func TestRegisterHealth_OptIn(t *testing.T) {
	t.Parallel()
	off := grpc.NewServer()
	require.Nil(t, registerHealth(off, false, nil))
	require.NotContains(t, off.GetServiceInfo(), healthService)

	on := grpc.NewServer()
	require.NotNil(t, registerHealth(on, true, nil))
	require.Contains(t, on.GetServiceInfo(), healthService)
}

// TestModule_HealthFromConfig wires grpc.health through fx with a registry.
func TestModule_HealthFromConfig(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{})
	require.NoError(t, err)
	ready := &atomic.Bool{}
	ready.Store(true)

	var srv *grpc.Server
	app := fxtest.New(
		t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		fx.Provide(func() *statuspage.Registry { return readinessRegistry(ready) }),
		Module,
		fx.Decorate(func(c Config) Config { c.Listen, c.Health = "127.0.0.1:0", true; return c }),
		fx.Populate(&srv),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, app.Start(ctx))
	require.Contains(t, srv.GetServiceInfo(), healthService)
	require.NoError(t, app.Stop(ctx))
}
