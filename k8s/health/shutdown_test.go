// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package health_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/k8s/health"
	"github.com/golusoris/golusoris/observability/statuspage"
)

const testWait = 5 * time.Second

func probe(t *testing.T, h http.Handler, path string) int {
	t.Helper()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
	return rr.Code
}

func gateMux(gate *health.ShutdownGate) *http.ServeMux {
	reg := statuspage.NewRegistry(clock.NewFake())
	reg.Register(gate.Check(health.ShutdownCheckName))
	reg.Register(statuspage.Check{
		Name: "heartbeat",
		Tags: []string{health.TagLiveness},
		Fn:   func(context.Context) error { return nil },
	})
	mux := http.NewServeMux()
	health.MountMux(mux, reg)
	return mux
}

// drainAsync runs gate.Drain on a goroutine and returns its result channel.
func drainAsync(ctx context.Context, gate *health.ShutdownGate) <-chan error {
	done := make(chan error, 1)
	go func() { done <- gate.Drain(ctx) }()
	return done
}

func TestShutdownGateFailsReadinessOnly(t *testing.T) {
	t.Parallel()
	gate := health.NewShutdownGate(clock.NewFake(), time.Second)
	mux := gateMux(gate)

	require.Equal(t, http.StatusOK, probe(t, mux, "/readyz"))
	require.True(t, gate.Begin())
	require.True(t, gate.ShuttingDown())
	require.Equal(t, http.StatusServiceUnavailable, probe(t, mux, "/readyz"))
	require.Equal(t, http.StatusOK, probe(t, mux, "/livez"))
	require.Equal(t, http.StatusOK, probe(t, mux, "/startupz"))
	require.False(t, gate.Begin(), "second Begin must report shutdown already began")
}

func TestShutdownGateDrainWaitsForDelay(t *testing.T) {
	t.Parallel()
	fc := clock.NewFake()
	gate := health.NewShutdownGate(fc, testWait)
	ctx, cancel := context.WithTimeout(t.Context(), testWait)
	defer cancel()

	done := drainAsync(ctx, gate)
	require.NoError(t, fc.BlockUntilContext(ctx, 1))
	require.True(t, gate.ShuttingDown())
	select {
	case err := <-done:
		t.Fatalf("Drain returned before the delay elapsed: %v", err)
	default:
	}
	fc.Advance(testWait)
	require.NoError(t, <-done)

	// The window is shared: a later caller returns at once, even on a dead ctx.
	dead, stop := context.WithCancel(t.Context())
	stop()
	require.NoError(t, gate.Drain(dead))
}

func TestShutdownGateDrainBoundedByContext(t *testing.T) {
	t.Parallel()
	gate := health.NewShutdownGate(clock.NewFake(), time.Hour)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	err := gate.Drain(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.True(t, gate.ShuttingDown(), "readiness must fail even when the drain is cut short")
}

func TestShutdownGateZeroDelayReturnsAtOnce(t *testing.T) {
	t.Parallel()
	gate := health.NewShutdownGate(clock.NewFake(), 0)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.NoError(t, gate.Drain(ctx))
	require.True(t, gate.ShuttingDown())
	require.Equal(t, http.StatusServiceUnavailable, probe(t, gateMux(gate), "/readyz"))
}

func TestNewShutdownGateClampsNegativeDelayAndNilClock(t *testing.T) {
	t.Parallel()
	gate := health.NewShutdownGate(nil, -time.Second)
	require.Zero(t, gate.Delay())
	require.NoError(t, gate.Drain(t.Context()))
}

func TestNilShutdownGateIsInert(t *testing.T) {
	t.Parallel()
	var gate *health.ShutdownGate
	require.NoError(t, gate.Drain(t.Context()))

	stopped := false
	hook := gate.Wrap(fx.Hook{OnStop: func(context.Context) error { stopped = true; return nil }})
	require.NoError(t, hook.OnStop(t.Context()))
	require.True(t, stopped)
}

func TestWrapRunsStopAfterDrainAndJoinsErrors(t *testing.T) {
	t.Parallel()
	gate := health.NewShutdownGate(clock.NewFake(), time.Hour)
	errStop := errors.New("stop failed")
	sawShutdown := false
	hook := gate.Wrap(fx.Hook{OnStop: func(context.Context) error {
		sawShutdown = gate.ShuttingDown()
		return errStop
	}})
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	err := hook.OnStop(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorIs(t, err, errStop, "a cut-short drain must still run the wrapped stop")
	require.True(t, sawShutdown)

	startOnly := gate.Wrap(fx.Hook{})
	require.Nil(t, startOnly.OnStop)
}

func TestDefaultOptions(t *testing.T) {
	t.Parallel()
	require.Equal(t, health.DefaultDrainDelay, health.DefaultOptions().Drain.Delay)
}

// fixedClock pins the fake clock so tests can advance it.
func fixedClock(fc *clockwork.FakeClock) fx.Option {
	return fx.Provide(func() clock.Clock { return fc })
}
