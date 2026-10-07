// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package health_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/k8s/health"
	"github.com/golusoris/golusoris/observability/statuspage"
)

// configWithDelay returns a config whose health.drain.delay is delay; empty
// delay leaves the key unset.
func configWithDelay(t *testing.T, delay string) *config.Config {
	t.Helper()
	opts := config.Options{EnvPrefix: "GOLUSORIS_HEALTH_TEST_"}
	if delay != "" {
		path := filepath.Join(t.TempDir(), "config.yaml")
		body := "health:\n  drain:\n    delay: " + delay + "\n"
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		opts.Files = []string{path}
	}
	cfg, err := config.New(opts)
	require.NoError(t, err)
	return cfg
}

func baseOptions(t *testing.T, fc *clockwork.FakeClock, delay string) fx.Option {
	t.Helper()
	cfg := configWithDelay(t, delay)
	return fx.Options(
		fx.NopLogger,
		fx.Supply(cfg),
		fixedClock(fc),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		fx.Provide(statuspage.NewRegistry),
	)
}

func TestModuleDrainDelayFromConfig(t *testing.T) {
	t.Parallel()
	cases := map[string]time.Duration{
		"":    health.DefaultDrainDelay, // unset key keeps the default
		"0s":  0,                        // explicit zero disables the wait
		"12s": 12 * time.Second,
	}
	for raw, want := range cases {
		t.Run("delay="+raw, func(t *testing.T) {
			t.Parallel()
			var gate *health.ShutdownGate
			app := fx.New(baseOptions(t, clock.NewFake(), raw), health.Module, fx.Populate(&gate))
			require.NoError(t, app.Err())
			require.Equal(t, want, gate.Delay())
		})
	}
}

func TestModuleRejectsNegativeDrainDelay(t *testing.T) {
	t.Parallel()
	app := fx.New(baseOptions(t, clock.NewFake(), "-1s"), health.Module, fx.Invoke(func(*health.ShutdownGate) {}))
	require.ErrorIs(t, app.Err(), health.ErrNegativeDrainDelay)
}

func TestModuleRequiresRegistry(t *testing.T) {
	t.Parallel()
	app := fx.New(
		fx.NopLogger,
		fx.Supply(configWithDelay(t, "")),
		fixedClock(clock.NewFake()),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		health.Module,
	)
	require.ErrorContains(t, app.Err(), "statuspage.Registry")
}

type stopEvent struct {
	at    time.Time
	ready int
}

type stopRecorder struct {
	mu     sync.Mutex
	events []stopEvent
}

func (r *stopRecorder) add(e stopEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *stopRecorder) snapshot() []stopEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]stopEvent(nil), r.events...)
}

type fakeServer struct{}

type fakeServerParams struct {
	fx.In

	Lifecycle fx.Lifecycle
	Gate      *health.ShutdownGate `optional:"true"`
	Registry  *statuspage.Registry
	Clock     clock.Clock
	Recorder  *stopRecorder
}

// newFakeServer mirrors httpx/server and grpc: its stop hook is gate-wrapped.
func newFakeServer(wrap bool) func(fakeServerParams) *fakeServer {
	return func(p fakeServerParams) *fakeServer {
		hook := fx.Hook{OnStop: func(ctx context.Context) error {
			w := httptest.NewRecorder()
			health.ReadyzHandler(p.Registry)(w, httptest.NewRequestWithContext(ctx, http.MethodGet, "/readyz", nil))
			p.Recorder.add(stopEvent{at: p.Clock.Now(), ready: w.Code})
			return nil
		}}
		if wrap {
			hook = p.Gate.Wrap(hook)
		}
		p.Lifecycle.Append(hook)
		return &fakeServer{}
	}
}

// stopWithDrain starts app, stops it while advancing the fake clock past the
// drain window.
func stopWithDrain(t *testing.T, fc *clockwork.FakeClock, app *fx.App) {
	t.Helper()
	require.NoError(t, app.Err())
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, app.Start(ctx))

	stopped := make(chan error, 1)
	go func() { stopped <- app.Stop(ctx) }()
	require.NoError(t, fc.BlockUntilContext(ctx, 1))
	fc.Advance(testWait)
	require.NoError(t, <-stopped)
}

func TestModuleServerStopsAfterReadinessDrain(t *testing.T) {
	t.Parallel()
	orders := map[string]func(fx.Option) []fx.Option{
		// Root invokes run after module invokes: server hook appended after the gate's.
		"server constructed after health": func(server fx.Option) []fx.Option {
			return []fx.Option{health.Module, server, fx.Invoke(func(*fakeServer) {})}
		},
		"server constructed before health": func(server fx.Option) []fx.Option {
			return []fx.Option{fx.Module("app", server, fx.Invoke(func(*fakeServer) {})), health.Module}
		},
	}
	for name, order := range orders {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fc := clock.NewFake()
			start := fc.Now()
			rec := &stopRecorder{}
			opts := append([]fx.Option{baseOptions(t, fc, testWait.String()), fx.Supply(rec)},
				order(fx.Provide(newFakeServer(true)))...)
			stopWithDrain(t, fc, fx.New(opts...))

			events := rec.snapshot()
			require.Len(t, events, 1)
			require.Equal(t, http.StatusServiceUnavailable, events[0].ready, "readiness must fail before the server stops")
			require.False(t, events[0].at.Before(start.Add(testWait)), "server stopped inside the drain window")
		})
	}
}

// TestModuleUnwrappedHookStopsBeforeDrain is the planted-defect control: a
// stop hook that skips Wrap and runs first stops while still ready.
func TestModuleUnwrappedHookStopsBeforeDrain(t *testing.T) {
	t.Parallel()
	fc := clock.NewFake()
	start := fc.Now()
	rec := &stopRecorder{}
	stopWithDrain(t, fc, fx.New(
		baseOptions(t, fc, testWait.String()),
		fx.Supply(rec),
		health.Module,
		fx.Provide(newFakeServer(false)),
		fx.Invoke(func(*fakeServer) {}),
	))

	events := rec.snapshot()
	require.Len(t, events, 1)
	require.Equal(t, http.StatusOK, events[0].ready)
	require.Equal(t, start, events[0].at)
}
