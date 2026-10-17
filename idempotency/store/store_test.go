// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package store_test

import (
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/idempotency/internal/storetest"
	"github.com/golusoris/golusoris/idempotency/store"
)

func newMemoryHarness(t *testing.T) storetest.Harness {
	t.Helper()
	clk := clockwork.NewFakeClockAt(storetest.Base())
	return storetest.Harness{
		Store:   store.NewMemoryStoreWithClock(clk),
		Advance: func(_ *testing.T, d time.Duration) { clk.Advance(d) },
	}
}

func TestMemoryStore_Conformance(t *testing.T) {
	t.Parallel()
	storetest.Run(t, newMemoryHarness)
}

func TestMemoryStore_Sweep(t *testing.T) {
	t.Parallel()
	h := newMemoryHarness(t)
	sweeper, ok := h.Store.(store.Sweeper)
	if !ok {
		t.Fatal("MemoryStore does not implement Sweeper")
	}
	storetest.RunSweep(t, h, sweeper)
}
