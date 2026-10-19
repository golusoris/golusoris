// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package goenvoy

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/golusoris/golusoris/cache/singleflight"
)

type deadlineFixture struct {
	mu      sync.RWMutex
	clients map[string]*int
	builds  *singleflight.Group[string, *int]
	calls   int
}

func newDeadlineFixture() *deadlineFixture {
	return &deadlineFixture{clients: map[string]*int{}, builds: singleflight.New[string, *int]()}
}

func (f *deadlineFixture) lookup(timeout, buildTime time.Duration) (*int, error) {
	return registryClient(&f.mu, f.clients, f.builds, "svc", timeout, func() (*int, error) {
		f.calls++
		time.Sleep(buildTime)
		value := f.calls
		return &value, nil
	})
}

// TestRegistryClientBuildDeadline covers the build bound: positive (a build
// inside the deadline is cached), negative (an overrun build is dropped), and
// boundary (a zero deadline refuses before building).
func TestRegistryClientBuildDeadline(t *testing.T) {
	t.Parallel()

	t.Run("inside deadline is cached", func(t *testing.T) {
		t.Parallel()
		f := newDeadlineFixture()
		first, err := f.lookup(time.Minute, 0)
		if err != nil {
			t.Fatalf("lookup: %v", err)
		}
		second, err := f.lookup(time.Minute, 0)
		if err != nil || second != first || f.calls != 1 {
			t.Fatalf("second lookup = (%p, %v), calls %d; want cached %p, 1 call", second, err, f.calls, first)
		}
	})

	t.Run("overrun build is dropped", func(t *testing.T) {
		t.Parallel()
		f := newDeadlineFixture()
		if _, err := f.lookup(time.Millisecond, 50*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v, want context.DeadlineExceeded", err)
		}
		if len(f.clients) != 0 {
			t.Fatalf("overrun build cached %d clients; want 0", len(f.clients))
		}
	})

	t.Run("zero deadline refuses before building", func(t *testing.T) {
		t.Parallel()
		f := newDeadlineFixture()
		if _, err := f.lookup(0, 0); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v, want context.DeadlineExceeded", err)
		}
		if f.calls != 0 {
			t.Fatalf("build ran %d times; want 0", f.calls)
		}
	})
}
