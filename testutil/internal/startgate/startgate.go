// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package startgate bounds how many testcontainers one test binary boots at
// once. Every testutil container helper takes a slot before it starts its
// container and gives it back when it returns.
//
// Without it, a package whose tests call t.Parallel and each boot their own
// container starts all of them together: ai/tiny booted ten Postgres
// containers at once, and on a CPU-capped CI runner none of them logged
// ready inside testcontainers' 60 s wait. `go test -p` bounds packages, not
// the tests inside one, so the bound has to live here.
package startgate

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

const (
	// Limit is how many containers one test binary may boot concurrently.
	// With `go test -p 4` in CI that caps the whole run at eight boots.
	Limit = 2

	// acquireTimeout bounds the wait for a slot (HISS-02). It matches the
	// per-package `go test -timeout=10m`, so a queued test is never failed
	// by the gate before the package itself would be.
	acquireTimeout = 10 * time.Minute
)

// boot is the process-wide gate shared by every testutil helper in a binary.
var boot = newGate(Limit)

// Acquire blocks until a boot slot is free and returns the function that
// frees it. Call it immediately before starting a container, and release it
// once the helper has finished booting:
//
//	defer startgate.Acquire(t)()
//
// It fails the test if no slot frees up within the package test timeout.
func Acquire(tb testing.TB) (release func()) {
	tb.Helper()
	ctx, cancel := context.WithTimeout(tb.Context(), acquireTimeout)
	defer cancel()
	release, err := boot.acquire(ctx)
	if err != nil {
		tb.Fatalf("testutil: %v", err)
	}
	return release
}

type gate struct {
	slots chan struct{}
}

func newGate(limit int) *gate {
	return &gate{slots: make(chan struct{}, limit)}
}

// acquire takes a slot or reports why it could not. The returned release is
// idempotent, so a helper that releases on more than one path cannot free a
// slot it does not hold.
func (g *gate) acquire(ctx context.Context) (func(), error) {
	select {
	case g.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("startgate: wait for a container boot slot: %w", ctx.Err())
	}
	var once sync.Once
	return func() { once.Do(func() { <-g.slots }) }, nil
}
