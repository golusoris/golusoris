// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Ported from SnatchArr api/internal/leaderx at 6111de5a.

package gate

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/golusoris/golusoris/core/clock"
)

// maxTicks bounds a periodic loop (HISS-02): 2^30 ticks at one a second is about 34 years.
const maxTicks = 1 << 30

// Observer receives the outcome of every tick, so that a failed tick reaches a metric and not
// only the log.
type Observer interface {
	LoopTicked(name string, interval time.Duration, at time.Time, err error)
}

// Periodic is the Loop every timer-driven leader job uses: it calls tick at once and then
// once per interval, each call under a deadline of one interval, until Stop. The gate may
// start it again after a stop, as leadership comes and goes.
type Periodic struct {
	name     string
	interval time.Duration
	clk      clock.Clock
	obs      Observer
	tick     func(ctx context.Context) error

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewPeriodic builds a loop. name prefixes its errors and names it to obs. tick logs the
// detail of its own failure; obs receives the error tick returns and the time it ended,
// except for a tick that Stop cut short.
func NewPeriodic(name string, interval time.Duration, clk clock.Clock, obs Observer, tick func(ctx context.Context) error) *Periodic {
	return &Periodic{name: name, interval: interval, clk: clk, obs: obs, tick: tick}
}

// Start launches the loop; the first tick runs at once. It refuses a second start while the
// loop runs.
func (p *Periodic) Start(parent context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cancel != nil {
		return fmt.Errorf("leader/gate: %s: loop is already running", p.name)
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	p.cancel, p.done = cancel, make(chan struct{})
	go p.loop(ctx, p.done)
	return nil
}

// Stop ends the loop and waits for the tick in flight, at most until ctx ends. Stopping a
// loop that does not run is a no-op.
func (p *Periodic) Stop(ctx context.Context) error {
	p.mu.Lock()
	cancel, done := p.cancel, p.done
	p.cancel, p.done = nil, nil
	p.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("leader/gate: %s: stop: %w", p.name, ctx.Err())
	}
}

// Name is the name the loop was built with.
func (p *Periodic) Name() string { return p.name }

// Running reports whether the loop is started.
func (p *Periodic) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cancel != nil
}

func (p *Periodic) loop(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	timer := p.clk.NewTicker(p.interval)
	defer timer.Stop()
	p.once(ctx)
	for range maxTicks {
		select {
		case <-ctx.Done():
			return
		case <-timer.Chan():
			p.once(ctx)
		}
	}
}

func (p *Periodic) once(ctx context.Context) {
	tctx, cancel := context.WithTimeout(ctx, p.interval)
	defer cancel()
	err := p.tick(tctx)
	if ctx.Err() != nil {
		// Stop ended the tick: the replica gave up leading, the tick did not fail.
		return
	}
	if p.obs != nil {
		p.obs.LoopTicked(p.name, p.interval, p.clk.Now(), err)
	}
}
