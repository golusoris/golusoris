// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package retry_test

import (
	"context"
	"errors"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/retry"
)

var errFlaky = errors.New("flaky")

func exactPolicy(attempts int) retry.Policy {
	return retry.Policy{Initial: 100 * time.Millisecond, Max: 250 * time.Millisecond, Multiplier: 2, MaxAttempts: attempts}
}

// runAsync starts Do on a fake clock and returns its result channel.
func runAsync(ctx context.Context, fn func(context.Context) error, p retry.Policy, fc clock.Clock) <-chan error {
	done := make(chan error, 1)
	go func() { done <- retry.Do(ctx, fn, p, fc) }()
	return done
}

// advanceExactly proves the wait is d: one nanosecond short keeps the
// timer pending, the remainder fires it.
func advanceExactly(ctx context.Context, t *testing.T, fc interface {
	BlockUntilContext(context.Context, int) error
	Advance(time.Duration)
}, calls *atomic.Int32, before int32, d time.Duration,
) {
	t.Helper()
	if err := fc.BlockUntilContext(ctx, 1); err != nil {
		t.Fatalf("waiting for backoff timer: %v", err)
	}
	fc.Advance(d - time.Nanosecond)
	if got := calls.Load(); got != before {
		t.Fatalf("attempt %d ran before the %v backoff elapsed", got, d)
	}
	fc.Advance(time.Nanosecond)
}

func TestDoBacksOffExponentiallyAndCaps(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	fc := clock.NewFake()
	var calls atomic.Int32
	done := runAsync(ctx, func(context.Context) error {
		if calls.Add(1) < 4 {
			return errFlaky
		}
		return nil
	}, exactPolicy(4), fc)

	for i, d := range []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 250 * time.Millisecond} {
		advanceExactly(ctx, t, fc, &calls, int32(i+1), d)
	}
	if err := <-done; err != nil {
		t.Fatalf("Do = %v, want success on attempt 4", err)
	}
	if calls.Load() != 4 {
		t.Fatalf("calls = %d, want 4", calls.Load())
	}
}

func TestDoExhaustsAttempts(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	fc := clock.NewFake()
	var calls atomic.Int32
	done := runAsync(ctx, func(context.Context) error { calls.Add(1); return errFlaky }, exactPolicy(2), fc)
	advanceExactly(ctx, t, fc, &calls, 1, 100*time.Millisecond)
	err := <-done
	if !errors.Is(err, retry.ErrExhausted) || !errors.Is(err, errFlaky) || calls.Load() != 2 {
		t.Fatalf("Do = %v after %d calls, want ErrExhausted wrapping flaky after 2", err, calls.Load())
	}
}

func TestDoSingleAttemptNeverWaits(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := retry.Do(ctx, func(context.Context) error { return errFlaky }, exactPolicy(1), clock.NewFake())
	if !errors.Is(err, retry.ErrExhausted) {
		t.Fatalf("Do = %v, want ErrExhausted without any wait", err)
	}
}

func TestDoStopsOnPermanentError(t *testing.T) {
	t.Parallel()
	errFatal := errors.New("fatal")
	p := exactPolicy(5)
	p.Retryable = func(err error) bool { return !errors.Is(err, errFatal) }
	// A bounded ctx turns a wrongly scheduled retry into a failure, not a hang.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var calls int
	err := retry.Do(ctx, func(context.Context) error { calls++; return errFatal }, p, clock.NewFake())
	if !errors.Is(err, retry.ErrPermanent) || !errors.Is(err, errFatal) || calls != 1 {
		t.Fatalf("Do = %v after %d calls, want ErrPermanent after 1", err, calls)
	}
}

func TestDoHonorsContextDuringWait(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	fc := clock.NewFake()
	done := runAsync(ctx, func(context.Context) error { return errFlaky }, exactPolicy(3), fc)
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer waitCancel()
	if err := fc.BlockUntilContext(waitCtx, 1); err != nil {
		t.Fatal(err)
	}
	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) || !errors.Is(err, errFlaky) {
		t.Fatalf("Do = %v, want context.Canceled with the last error", err)
	}
}

func TestDoDoneContextSkipsOperation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := retry.Do(ctx, func(context.Context) error { called = true; return nil }, retry.DefaultPolicy(), clock.NewFake())
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("Do = %v, called = %v; want context.Canceled before any call", err, called)
	}
}

func TestDoJitterStaysWithinBounds(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	fc := clock.NewFake()
	p := exactPolicy(2)
	p.Jitter = 1
	var calls atomic.Int32
	done := runAsync(ctx, func(context.Context) error {
		if calls.Add(1) == 1 {
			return errFlaky
		}
		return nil
	}, p, fc)
	if err := fc.BlockUntilContext(ctx, 1); err != nil {
		t.Fatal(err)
	}
	fc.Advance(p.Initial) // full jitter draws from [0, Initial]
	if err := <-done; err != nil || calls.Load() != 2 {
		t.Fatalf("Do = %v after %d calls; jittered delay exceeded Initial", err, calls.Load())
	}
}

func TestPolicyValidate(t *testing.T) {
	t.Parallel()
	if err := retry.DefaultPolicy().Validate(); err != nil {
		t.Fatalf("DefaultPolicy invalid: %v", err)
	}
	boundary := retry.Policy{Initial: time.Nanosecond, Max: time.Nanosecond, Multiplier: 1, Jitter: 1, MaxAttempts: 1}
	if err := boundary.Validate(); err != nil {
		t.Fatalf("boundary policy rejected: %v", err)
	}
	mutate := map[string]func(*retry.Policy){
		"zero attempts":    func(p *retry.Policy) { p.MaxAttempts = 0 },
		"zero initial":     func(p *retry.Policy) { p.Initial = 0 },
		"max below init":   func(p *retry.Policy) { p.Max = p.Initial - 1 },
		"multiplier < 1":   func(p *retry.Policy) { p.Multiplier = 0.99 },
		"multiplier NaN":   func(p *retry.Policy) { p.Multiplier = math.NaN() },
		"negative jitter":  func(p *retry.Policy) { p.Jitter = -0.01 },
		"jitter above one": func(p *retry.Policy) { p.Jitter = 1.01 },
	}
	for name, change := range mutate {
		p := retry.DefaultPolicy()
		change(&p)
		called := false
		err := retry.Do(t.Context(), func(context.Context) error { called = true; return nil }, p, clock.NewFake())
		if !errors.Is(err, retry.ErrInvalidPolicy) || called {
			t.Errorf("%s: Do = %v, called = %v", name, err, called)
		}
	}
	if err := retry.Do(t.Context(), nil, retry.DefaultPolicy(), clock.NewFake()); !errors.Is(err, retry.ErrInvalidPolicy) {
		t.Errorf("nil operation: %v", err)
	}
	if err := retry.Do(t.Context(), func(context.Context) error { return nil }, retry.DefaultPolicy(), nil); !errors.Is(err, retry.ErrInvalidPolicy) {
		t.Errorf("nil clock: %v", err)
	}
}
