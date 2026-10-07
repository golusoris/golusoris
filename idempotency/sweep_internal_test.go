// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package idempotency

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx/fxtest"
)

// scriptedSweeper returns removed per call, or err when set.
type scriptedSweeper struct {
	removed  int64
	err      error
	calls    atomic.Int32
	deadline atomic.Bool
}

func (s *scriptedSweeper) Sweep(ctx context.Context, _ int) (int64, error) {
	s.calls.Add(1)
	_, ok := ctx.Deadline()
	s.deadline.Store(ok)
	return s.removed, s.err
}

func newLoop(sweeper Sweeper, logger *slog.Logger) sweepLoop {
	return sweepLoop{
		sweeper: sweeper,
		cfg:     SweepConfig{Interval: time.Minute, Batch: 10, Timeout: time.Second},
		clk:     clockwork.NewFakeClock(),
		logger:  logger,
	}
}

func TestSweepLoopTick(t *testing.T) {
	t.Parallel()
	closed := make(chan struct{})
	close(closed)
	tests := map[string]struct {
		removed   int64
		err       error
		stop      chan struct{}
		wantCalls int32
		wantTotal int64
	}{
		"short batch stops":         {removed: 3, wantCalls: 1, wantTotal: 3},
		"full batches stop at cap":  {removed: 10, wantCalls: maxSweepBatchesPerTick, wantTotal: 10 * maxSweepBatchesPerTick},
		"stop ends after one batch": {removed: 10, stop: closed, wantCalls: 1, wantTotal: 10},
		"error stops and is logged": {err: errors.New("db down"), wantCalls: 1},
		"empty table sweeps once":   {wantCalls: 1},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			sweeper := &scriptedSweeper{removed: tc.removed, err: tc.err}
			stop := tc.stop
			if stop == nil {
				stop = make(chan struct{})
			}
			total := newLoop(sweeper, slog.New(slog.NewTextHandler(&logs, nil))).tick(stop)
			require.Equal(t, tc.wantTotal, total)
			require.Equal(t, tc.wantCalls, sweeper.calls.Load())
			require.True(t, sweeper.deadline.Load(), "every batch runs under a deadline")
			require.Equal(t, tc.err != nil, bytes.Contains(logs.Bytes(), []byte("db down")))
		})
	}
}

func TestRegisterSweeper_RunsEveryIntervalAndStops(t *testing.T) {
	t.Parallel()
	clk := clockwork.NewFakeClock()
	store := &sweepingStore{MemoryStore: NewMemoryStore()}
	lc := fxtest.NewLifecycle(t)
	cfg := defaultOptions()
	registerSweeper(lc, store, cfg, clk, slog.New(slog.DiscardHandler))
	lc.RequireStart()
	for want := int32(1); want <= 2; want++ {
		require.NoError(t, clk.BlockUntilContext(t.Context(), 1))
		clk.Advance(cfg.Sweep.Interval)
		require.Eventually(t, func() bool { return store.calls.Load() == want }, 5*time.Second, time.Millisecond)
	}
	lc.RequireStop()
}

func TestRegisterSweeper_SkipsStoresWithoutSweeper(t *testing.T) {
	t.Parallel()
	lc := fxtest.NewLifecycle(t)
	registerSweeper(lc, plainStore{}, defaultOptions(), clockwork.NewFakeClock(), slog.New(slog.DiscardHandler))
	lc.RequireStart()
	lc.RequireStop()
}

// sweepingStore counts Sweep calls on top of a MemoryStore.
type sweepingStore struct {
	*MemoryStore

	calls atomic.Int32
}

func (s *sweepingStore) Sweep(ctx context.Context, limit int) (int64, error) {
	s.calls.Add(1)
	return s.MemoryStore.Sweep(ctx, limit)
}

// plainStore implements Store without Sweeper.
type plainStore struct{ Store }
