// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package retry runs an operation with capped exponential backoff and
// jitter. Waits run on an injected [clock.Clock] and end early when the
// context is done, so retries never outlive their caller and tests advance
// a fake clock instead of sleeping.
//
//	err := retry.Do(ctx, func(ctx context.Context) error {
//	    return client.Ping(ctx)
//	}, retry.DefaultPolicy(), clk)
package retry

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/golusoris/golusoris/core/clock"
)

// Policy configures [Do]. Every field except Jitter and Retryable must be set.
type Policy struct {
	// Initial is the delay after the first failure; must be positive.
	Initial time.Duration
	// Max caps every delay; must be at least Initial.
	Max time.Duration
	// Multiplier grows the delay after each failure; must be at least 1.
	Multiplier float64
	// Jitter in [0, 1] draws each delay from [d*(1-Jitter), d], so Max
	// stays a hard cap while concurrent callers spread out.
	Jitter float64
	// MaxAttempts counts calls to the operation, the first included; must
	// be at least 1.
	MaxAttempts int
	// Retryable reports whether an error is worth another attempt; nil
	// retries every error.
	Retryable func(error) bool
}

// DefaultPolicy returns 5 attempts, 100ms doubling to 10s, 20% jitter.
func DefaultPolicy() Policy {
	return Policy{
		Initial:     100 * time.Millisecond,
		Max:         10 * time.Second,
		Multiplier:  2,
		Jitter:      0.2,
		MaxAttempts: 5,
	}
}

var (
	// ErrInvalidPolicy reports a Policy that fails validation; the
	// operation is never called.
	ErrInvalidPolicy = errors.New("retry: invalid policy")
	// ErrExhausted reports that every attempt failed; the last operation
	// error is wrapped alongside it.
	ErrExhausted = errors.New("retry: attempts exhausted")
	// ErrPermanent reports an error Retryable rejected; the operation error
	// is wrapped alongside it.
	ErrPermanent = errors.New("retry: permanent error")
)

// Validate reports whether p is usable by [Do].
func (p Policy) Validate() error {
	switch {
	case p.MaxAttempts < 1:
		return fmt.Errorf("%w: max attempts must be at least 1", ErrInvalidPolicy)
	case p.Initial <= 0:
		return fmt.Errorf("%w: initial delay must be positive", ErrInvalidPolicy)
	case p.Max < p.Initial:
		return fmt.Errorf("%w: max delay must be at least the initial delay", ErrInvalidPolicy)
	case math.IsNaN(p.Multiplier) || p.Multiplier < 1:
		return fmt.Errorf("%w: multiplier must be at least 1", ErrInvalidPolicy)
	case math.IsNaN(p.Jitter) || p.Jitter < 0 || p.Jitter > 1:
		return fmt.Errorf("%w: jitter must be within [0, 1]", ErrInvalidPolicy)
	}
	return nil
}

// Do calls fn until it succeeds, returns an error Retryable rejects, makes
// MaxAttempts calls, or ctx is done. Failures wrap [ErrPermanent] or
// [ErrExhausted] together with fn's error; a done context wraps ctx.Err()
// and the last error.
func Do(ctx context.Context, fn func(context.Context) error, p Policy, clk clock.Clock) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if fn == nil || clk == nil {
		return fmt.Errorf("%w: operation and clock are required", ErrInvalidPolicy)
	}
	delay := p.Initial
	var lastErr error
	for attempt := 1; attempt <= p.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return contextError(err, lastErr)
		}
		lastErr = fn(ctx)
		if final, err := p.settle(lastErr, attempt); final {
			return err
		}
		if err := wait(ctx, clk, jittered(delay, p.Jitter, unitRandom())); err != nil {
			return contextError(err, lastErr)
		}
		delay = grow(delay, p.Multiplier, p.Max)
	}
	return fmt.Errorf("%w after %d attempts: %w", ErrExhausted, p.MaxAttempts, lastErr)
}

// settle decides whether attempt's outcome ends Do and with which error.
func (p Policy) settle(err error, attempt int) (final bool, result error) {
	switch {
	case err == nil:
		return true, nil
	case p.Retryable != nil && !p.Retryable(err):
		return true, fmt.Errorf("%w after %d attempts: %w", ErrPermanent, attempt, err)
	case attempt >= p.MaxAttempts:
		return true, fmt.Errorf("%w after %d attempts: %w", ErrExhausted, attempt, err)
	default:
		return false, nil
	}
}

func contextError(ctxErr, lastErr error) error {
	if lastErr == nil {
		return fmt.Errorf("retry: %w", ctxErr)
	}
	return fmt.Errorf("retry: %w (last error: %w)", ctxErr, lastErr)
}

// wait blocks for d on clk, ending early when ctx is done.
func wait(ctx context.Context, clk clock.Clock, d time.Duration) error {
	timer := clk.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.Chan():
		return nil
	}
}

// grow multiplies delay, saturating at maximum instead of overflowing.
func grow(delay time.Duration, multiplier float64, maximum time.Duration) time.Duration {
	next := float64(delay) * multiplier
	if next >= float64(maximum) {
		return maximum
	}
	return time.Duration(next)
}

// jittered draws from [d*(1-jitter), d] using r in [0, 1).
func jittered(d time.Duration, jitter, r float64) time.Duration {
	return d - time.Duration(float64(d)*jitter*r)
}

// unitRandom returns a uniform value in [0, 1); crypto/rand avoids a
// shared seeded generator. A read failure only removes the jitter.
func unitRandom() float64 {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return 0
	}
	return float64(binary.LittleEndian.Uint64(buf[:])>>11) / (1 << 53)
}
