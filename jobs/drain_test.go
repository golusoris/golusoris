// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jobs_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golusoris/golusoris/jobs"
)

// fakeStopper records drain calls; blockSoft/blockHard wait for ctx expiry.
type fakeStopper struct {
	blockSoft, blockHard bool
	softDeadline         time.Duration
	hardCalls            atomic.Int32
}

func (f *fakeStopper) Stop(ctx context.Context) error {
	if dl, ok := ctx.Deadline(); ok {
		f.softDeadline = time.Until(dl)
	}
	if f.blockSoft {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (f *fakeStopper) StopAndCancel(ctx context.Context) error {
	f.hardCalls.Add(1)
	if f.blockHard {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestDrain_softStopCompletesWithoutCancel(t *testing.T) {
	t.Parallel()
	s := &fakeStopper{}
	if err := jobs.Drain(context.Background(), s, jobs.StopOptions{Soft: time.Second, Hard: time.Second}, discard()); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if got := s.hardCalls.Load(); got != 0 {
		t.Fatalf("StopAndCancel calls = %d, want 0", got)
	}
}

func TestDrain_softTimeoutEscalatesToHardStop(t *testing.T) {
	t.Parallel()
	s := &fakeStopper{blockSoft: true}
	if err := jobs.Drain(context.Background(), s, jobs.StopOptions{Soft: 20 * time.Millisecond, Hard: time.Second}, discard()); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if got := s.hardCalls.Load(); got != 1 {
		t.Fatalf("StopAndCancel calls = %d, want 1", got)
	}
}

func TestDrain_hardTimeoutReturnsError(t *testing.T) {
	t.Parallel()
	s := &fakeStopper{blockSoft: true, blockHard: true}
	err := jobs.Drain(context.Background(), s, jobs.StopOptions{Soft: 10 * time.Millisecond, Hard: 10 * time.Millisecond}, discard())
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "hard stop") {
		t.Fatalf("Drain error = %v, want hard-stop deadline", err)
	}
}

func TestDrain_expiredStopContextStillCancelsJobs(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &fakeStopper{blockSoft: true, blockHard: true}
	err := jobs.Drain(ctx, s, jobs.StopOptions{}, discard())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Drain error = %v, want context.Canceled", err)
	}
	if got := s.hardCalls.Load(); got != 1 {
		t.Fatalf("StopAndCancel calls = %d, want 1 even after the stop deadline", got)
	}
}

func TestDrain_zeroOptionsUseDefaultSoftTimeout(t *testing.T) {
	t.Parallel()
	s := &fakeStopper{}
	if err := jobs.Drain(context.Background(), s, jobs.StopOptions{}, discard()); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if s.softDeadline <= 9*time.Second || s.softDeadline > 10*time.Second {
		t.Fatalf("soft deadline = %v, want the 10s default", s.softDeadline)
	}
}

func TestDrain_rejectsInvalidInput(t *testing.T) {
	t.Parallel()
	var nilStopper *fakeStopper
	cases := map[string]struct {
		s      jobs.Stopper
		opts   jobs.StopOptions
		logger *slog.Logger
		want   string
	}{
		"nil client":    {s: nilStopper, logger: discard(), want: "nil client"},
		"nil logger":    {s: &fakeStopper{}, want: "nil logger"},
		"negative soft": {s: &fakeStopper{}, opts: jobs.StopOptions{Soft: -1}, logger: discard(), want: "negative"},
		"negative hard": {s: &fakeStopper{}, opts: jobs.StopOptions{Hard: -1}, logger: discard(), want: "negative"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := jobs.Drain(context.Background(), tc.s, tc.opts, tc.logger)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Drain error = %v, want %q", err, tc.want)
			}
		})
	}
}
