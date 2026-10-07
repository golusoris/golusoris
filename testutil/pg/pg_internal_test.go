// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pg

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
)

// fakeTerminator records the ctx it receives and runs fn under it.
type fakeTerminator struct {
	deadline    time.Time
	hasDeadline bool
	fn          func(ctx context.Context) error
}

func (f *fakeTerminator) Terminate(ctx context.Context, _ ...testcontainers.TerminateOption) error {
	f.deadline, f.hasDeadline = ctx.Deadline()
	return f.fn(ctx)
}

func TestTerminate_boundedContext(t *testing.T) {
	t.Parallel()
	fake := &fakeTerminator{fn: func(context.Context) error { return nil }}
	before := time.Now()
	if err := terminate(fake, terminateTimeout); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if !fake.hasDeadline {
		t.Fatal("Terminate received a context without a deadline")
	}
	if limit := before.Add(terminateTimeout); fake.deadline.After(limit.Add(time.Second)) {
		t.Fatalf("deadline %v exceeds start+terminateTimeout %v", fake.deadline, limit)
	}
}

func TestTerminate_hungTeardownTimesOut(t *testing.T) {
	t.Parallel()
	fake := &fakeTerminator{fn: func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}}
	const budget = 50 * time.Millisecond
	start := time.Now()
	err := terminate(fake, budget)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("terminate error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 20*budget {
		t.Fatalf("terminate returned after %v, want about %v", elapsed, budget)
	}
}

func TestTerminate_wrapsError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("remove failed")
	fake := &fakeTerminator{fn: func(context.Context) error { return sentinel }}
	if err := terminate(fake, terminateTimeout); !errors.Is(err, sentinel) {
		t.Fatalf("terminate error = %v, want wrapped sentinel", err)
	}
}

func TestTerminate_zeroTimeoutIsExpired(t *testing.T) {
	t.Parallel()
	fake := &fakeTerminator{fn: func(ctx context.Context) error { return ctx.Err() }}
	if err := terminate(fake, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("terminate error = %v, want context.DeadlineExceeded", err)
	}
}
