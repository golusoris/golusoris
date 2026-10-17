// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testctx_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/golusoris/golusoris/testutil/testctx"
)

type recordingTB struct {
	testing.TB
	parent context.Context
	fatal  string
}

func (tb *recordingTB) Context() context.Context { return tb.parent }

func (tb *recordingTB) Fatalf(format string, args ...any) {
	tb.Helper()
	tb.fatal = fmt.Sprintf(format, args...)
	panic("recorded Fatalf")
}

func TestDeadline(t *testing.T) {
	t.Parallel()
	parent := t
	t.Run("cleanup", func(t *testing.T) {
		t.Parallel()
		// Detached cancellation makes registered cleanup the only source of cancellation.
		tb := &recordingTB{TB: t, parent: context.WithoutCancel(t.Context())}
		const duration = time.Minute
		before := time.Now()
		ctx := testctx.Deadline(tb, duration)
		after := time.Now()
		deadline, ok := ctx.Deadline()
		if !ok || deadline.Before(before.Add(duration)) || deadline.After(after.Add(duration)) {
			t.Fatalf("Deadline() = %v, %v; want between %v and %v", deadline, ok, before.Add(duration), after.Add(duration))
		}
		if err := ctx.Err(); err != nil {
			t.Fatalf("live context error = %v", err)
		}
		parent.Cleanup(func() {
			if err := ctx.Err(); !errors.Is(err, context.Canceled) {
				parent.Fatalf("context after cleanup error = %v, want canceled", err)
			}
		})
	})
}

func TestDeadlineNonPositive(t *testing.T) {
	t.Parallel()
	for _, duration := range []time.Duration{0, -time.Second} {
		t.Run(duration.String(), func(t *testing.T) {
			t.Parallel()
			tb := &recordingTB{TB: t, parent: t.Context()}
			defer func() {
				if got := recover(); got != "recorded Fatalf" {
					t.Fatalf("Deadline() panic = %v, want recorded Fatalf", got)
				}
				want := fmt.Sprintf("testctx: deadline duration must be positive, got %v", duration)
				if tb.fatal != want {
					t.Fatalf("Fatalf message = %q, want %q", tb.fatal, want)
				}
			}()
			testctx.Deadline(tb, duration)
		})
	}
}

func TestDeadlineTinyDuration(t *testing.T) {
	t.Parallel()
	ctx := testctx.Deadline(t, time.Nanosecond)
	select {
	case <-ctx.Done():
		if err := ctx.Err(); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expired context error = %v, want DeadlineExceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("tiny deadline did not expire within one second")
	}
}
