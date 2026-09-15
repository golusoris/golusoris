// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package twotier

import (
	"errors"
	"testing"
	"time"

	"github.com/golusoris/golusoris/core/config"
)

func TestLoadOptions_defaults(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_TWOTIER_"})
	if err != nil {
		t.Fatal(err)
	}
	opts, err := loadOptions(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if opts.L1TTL != time.Minute {
		t.Errorf("L1TTL = %v, want 1m", opts.L1TTL)
	}
	if opts.L2TTL != 5*time.Minute {
		t.Errorf("L2TTL = %v, want 5m", opts.L2TTL)
	}
}

// TestRunBoundedScan_CompletesOnZeroCursor is the positive case: fn reports
// completion (cursor 0) well before the round bound, and runBoundedScan
// returns nil having driven exactly that many rounds.
func TestRunBoundedScan_CompletesOnZeroCursor(t *testing.T) {
	t.Parallel()
	var rounds []uint64
	err := runBoundedScan(10, func(cursor uint64) (uint64, error) {
		rounds = append(rounds, cursor)
		if len(rounds) < 3 {
			return uint64(len(rounds)), nil
		}
		return 0, nil
	})
	if err != nil {
		t.Fatalf("runBoundedScan() = %v, want nil", err)
	}
	want := []uint64{0, 1, 2}
	if len(rounds) != len(want) {
		t.Fatalf("rounds = %v, want %v", rounds, want)
	}
	for i, c := range want {
		if rounds[i] != c {
			t.Errorf("rounds[%d] = %d, want %d", i, rounds[i], c)
		}
	}
}

// TestRunBoundedScan_PropagatesRoundError is the negative case: a round-level
// error short-circuits the loop and is returned as-is.
func TestRunBoundedScan_PropagatesRoundError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("boom")
	calls := 0
	err := runBoundedScan(10, func(_ uint64) (uint64, error) {
		calls++
		return 0, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("runBoundedScan() = %v, want %v", err, wantErr)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (loop must stop on first error)", calls)
	}
}

// TestRunBoundedScan_BoundedAgainstNonTerminatingCursor is the boundary case
// (HISS-02): a server that never returns cursor 0 must not spin forever —
// runBoundedScan stops after maxRounds and reports the bound was exceeded.
func TestRunBoundedScan_BoundedAgainstNonTerminatingCursor(t *testing.T) {
	t.Parallel()
	const maxRounds = 5
	calls := 0
	err := runBoundedScan(maxRounds, func(cursor uint64) (uint64, error) {
		calls++
		return cursor + 1, nil // never reports completion
	})
	if err == nil {
		t.Fatal("runBoundedScan() = nil, want bound-exceeded error")
	}
	if calls != maxRounds {
		t.Errorf("calls = %d, want %d", calls, maxRounds)
	}
}
