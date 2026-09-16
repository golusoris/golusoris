// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package startgate

import (
	"context"
	"errors"
	"testing"
)

// Positive: a free gate hands out a slot, and releasing it frees the slot.
func TestGate_acquireAndRelease(t *testing.T) {
	t.Parallel()
	g := newGate(1)

	release, err := g.acquire(t.Context())
	if err != nil {
		t.Fatalf("acquire on a free gate: %v", err)
	}
	if got := len(g.slots); got != 1 {
		t.Fatalf("slots held after acquire = %d, want 1", got)
	}
	release()
	if got := len(g.slots); got != 0 {
		t.Fatalf("slots held after release = %d, want 0", got)
	}
}

// Boundary: exactly Limit slots are handed out without blocking; the next
// caller waits, and gets the slot as soon as one is released.
func TestGate_blocksAtLimitUntilReleased(t *testing.T) {
	t.Parallel()
	g := newGate(Limit)

	releases := make([]func(), 0, Limit)
	for i := range Limit {
		release, err := g.acquire(t.Context())
		if err != nil {
			t.Fatalf("acquire %d of %d: %v", i+1, Limit, err)
		}
		releases = append(releases, release)
	}

	full, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := g.acquire(full); !errors.Is(err, context.Canceled) {
		t.Fatalf("acquire past the limit = %v, want context.Canceled", err)
	}

	got := make(chan error, 1)
	go func() {
		release, err := g.acquire(t.Context())
		if err == nil {
			release()
		}
		got <- err
	}()
	releases[0]()
	if err := <-got; err != nil {
		t.Fatalf("waiting acquire after a release: %v", err)
	}
	for _, release := range releases[1:] {
		release()
	}
}

// Negative: a context that ends while the gate is full is an error, not a
// slot, and it names what it was waiting for.
func TestGate_contextEndsWhileFull(t *testing.T) {
	t.Parallel()
	g := newGate(1)
	release, err := g.acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	rel, err := g.acquire(ctx)
	if err == nil || rel != nil {
		t.Fatalf("acquire on a full gate with an ended context = (%v, %v), want (nil, error)", rel != nil, err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want it to wrap context.Canceled", err)
	}
}

// Negative: releasing twice frees only the one slot that was taken, so a
// double release cannot let an extra container boot.
func TestGate_releaseIsIdempotent(t *testing.T) {
	t.Parallel()
	g := newGate(2)

	first, err := g.acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	second, err := g.acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	first()
	first()
	if got := len(g.slots); got != 1 {
		t.Fatalf("slots held after double release of one slot = %d, want 1", got)
	}
	second()
}

// Positive: the exported helper takes and returns a slot on the shared gate.
// The only test that touches boot; every other test builds its own gate.
func TestAcquire_usesSharedGate(t *testing.T) {
	t.Parallel()
	before := len(boot.slots)
	release := Acquire(t)
	if got := len(boot.slots); got != before+1 {
		t.Fatalf("shared slots held = %d, want %d", got, before+1)
	}
	release()
	if got := len(boot.slots); got != before {
		t.Fatalf("shared slots after release = %d, want %d", got, before)
	}
}
