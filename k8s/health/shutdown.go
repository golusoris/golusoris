// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package health

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jonboulle/clockwork"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/core/drain"
	"github.com/golusoris/golusoris/core/validate"
	"github.com/golusoris/golusoris/observability/statuspage"
)

// DefaultDrainDelay is how long /readyz fails before servers stop, so endpoint
// removal reaches kube-proxy and load balancers before connections close.
const DefaultDrainDelay = 5 * time.Second

// ShutdownCheckName names the readiness check that [Module] registers.
const ShutdownCheckName = "shutdown"

// ErrShuttingDown is the readiness failure reported once shutdown has begun.
var ErrShuttingDown = errors.New("k8s/health: shutting down")

// ErrNegativeDrainDelay rejects a negative health.drain.delay.
var ErrNegativeDrainDelay = errors.New("k8s/health: drain delay must not be negative")

// ShutdownGate fails readiness once shutdown begins and holds component
// shutdown for a drain window. Liveness and startup are unaffected.
type ShutdownGate struct {
	clk    clock.Clock
	delay  time.Duration
	logger *slog.Logger

	mu    sync.Mutex
	began time.Time
	down  atomic.Bool
}

// NewShutdownGate returns an open gate with the given drain window. A nil
// clock uses the real clock; a negative delay is treated as zero.
func NewShutdownGate(clk clock.Clock, delay time.Duration) *ShutdownGate {
	if validate.IsNil(clk) {
		clk = clockwork.NewRealClock()
	}
	return &ShutdownGate{clk: clk, delay: max(delay, 0)}
}

// Delay returns the drain window.
func (g *ShutdownGate) Delay() time.Duration { return g.delay }

// Begin fails readiness and starts the drain window. Idempotent; reports
// whether this call began shutdown.
func (g *ShutdownGate) Begin() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.down.Load() {
		return false
	}
	g.began = g.clk.Now()
	g.down.Store(true)
	return true
}

// ShuttingDown reports whether shutdown has begun.
func (g *ShutdownGate) ShuttingDown() bool { return g.down.Load() }

// Drain begins shutdown and blocks until the drain window has elapsed. All
// callers share one window: the first pays the delay, later callers return
// once it ends. It returns the wrapped ctx error when ctx ends first. A nil
// gate returns nil.
func (g *ShutdownGate) Drain(ctx context.Context) error {
	if g == nil {
		return nil
	}
	if g.Begin() && g.logger != nil {
		g.logger.InfoContext(ctx, "k8s/health: shutdown began; readiness failing",
			slog.Duration("drain_delay", g.delay))
	}
	wait := g.remaining()
	if wait <= 0 {
		return nil
	}
	timer := g.clk.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.Chan():
		return nil
	case <-ctx.Done():
		return fmt.Errorf("k8s/health: drain: %w", ctx.Err())
	}
}

func (g *ShutdownGate) remaining() time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.delay - g.clk.Since(g.began)
}

// Wrap returns hook with OnStop deferred until the drain window ends, so the
// component stops only after readiness has failed for the drain delay. When
// ctx cuts the drain short, OnStop still runs and both errors are joined. A
// nil gate or a hook without OnStop is returned unchanged.
func (g *ShutdownGate) Wrap(hook fx.Hook) fx.Hook {
	if g == nil || hook.OnStop == nil {
		return hook
	}
	stop := hook.OnStop
	hook.OnStop = func(ctx context.Context) error {
		drainErr := g.Drain(ctx)
		return errors.Join(drainErr, stop(ctx))
	}
	return hook
}

// Check returns a readiness-tagged [statuspage.Check] that fails with
// [ErrShuttingDown] once shutdown has begun.
func (g *ShutdownGate) Check(name string) statuspage.Check {
	return statuspage.Check{
		Name: name,
		Tags: []string{TagReadiness},
		Fn: func(context.Context) error {
			if g.down.Load() {
				return ErrShuttingDown
			}
			return nil
		},
	}
}

// Options configures [Module]. Durations accept koanf strings like "5s".
type Options struct {
	Drain DrainOptions `koanf:"drain"`
}

// DrainOptions tunes the shutdown drain window.
type DrainOptions struct {
	// Delay is how long /readyz fails before servers stop; 0 disables the wait.
	Delay time.Duration `koanf:"delay"`
}

// DefaultOptions returns the defaults: a [DefaultDrainDelay] drain window.
func DefaultOptions() Options {
	return Options{Drain: DrainOptions{Delay: DefaultDrainDelay}}
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := DefaultOptions()
	if err := cfg.Unmarshal("health", &opts); err != nil {
		return Options{}, fmt.Errorf("k8s/health: load options: %w", err)
	}
	if opts.Drain.Delay < 0 {
		return Options{}, fmt.Errorf("%w: %s", ErrNegativeDrainDelay, opts.Drain.Delay)
	}
	return opts, nil
}

var _ drain.Gate = (*ShutdownGate)(nil)

// asDrainGate offers the gate to servers through core/drain, so they need not import this package.
func asDrainGate(g *ShutdownGate) drain.Gate { return g }

func newModuleGate(opts Options, clk clock.Clock, logger *slog.Logger) *ShutdownGate {
	gate := NewShutdownGate(clk, opts.Drain.Delay)
	gate.logger = logger
	return gate
}

type shutdownParams struct {
	fx.In

	Lifecycle fx.Lifecycle
	Registry  *statuspage.Registry
	Gate      *ShutdownGate
}

func registerShutdown(p shutdownParams) {
	p.Registry.Register(p.Gate.Check(ShutdownCheckName))
	p.Lifecycle.Append(fx.Hook{OnStop: p.Gate.Drain})
}

// Module provides a [*ShutdownGate] configured from health.drain.delay
// (env APP_HEALTH_DRAIN_DELAY, default 5s), registers its readiness check
// on the app's [*statuspage.Registry], and drains on fx Stop. It also provides
// the gate as a core/drain Gate: httpx/server and grpc wrap their stop hooks
// with it, so they stop only after the drain window regardless of module
// order; app components do the same with drain.Wrap. Requires *config.Config, clock.Clock, *slog.Logger,
// and a *statuspage.Registry (e.g. fx.Provide(statuspage.NewRegistry)).
var Module = fx.Module(
	"golusoris.k8s.health",
	fx.Provide(loadOptions, newModuleGate, asDrainGate),
	fx.Invoke(registerShutdown),
)
