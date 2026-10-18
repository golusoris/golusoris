// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package idempotency_test

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/idempotency"
	"github.com/golusoris/golusoris/idempotency/internal/storetest"
)

func acquire(t *testing.T, value idempotency.Store, key string) string {
	t.Helper()
	return storetest.Acquire(t, value, key)
}

func newMemoryHarness(t *testing.T) storetest.Harness {
	t.Helper()
	clk := clockwork.NewFakeClockAt(storetest.Base())
	return storetest.Harness{
		Store:   idempotency.NewMemoryStoreWithClock(clk),
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
	sweeper, ok := h.Store.(idempotency.Sweeper)
	if !ok {
		t.Fatal("MemoryStore does not implement Sweeper")
	}
	storetest.RunSweep(t, h, sweeper)
}

// TestMemoryStore_NotSharedAcrossReplicas is the negative of the shared-store
// acceptance: two processes with their own MemoryStore both execute.
func TestMemoryStore_NotSharedAcrossReplicas(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	newHandler := func() http.Handler {
		return idempotency.Middleware(idempotency.NewMemoryStore(), idempotency.Options{})(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusCreated)
			},
		))
	}
	sendIdempotentRequest(newHandler(), "/payments", "shared", "payload")
	sendIdempotentRequest(newHandler(), "/payments", "shared", "payload")
	if calls.Load() != 2 {
		t.Fatalf("handler calls = %d; want 2 with per-process stores", calls.Load())
	}
}
