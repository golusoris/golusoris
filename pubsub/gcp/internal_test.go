// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gcp

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/config"
)

// TestNewFromConfig_MissingProjectID is the negative case: an empty
// "pubsub.gcp.project_id" must fail fast, before any network I/O.
func TestNewFromConfig_MissingProjectID(t *testing.T) {
	t.Parallel()

	cfg, err := config.New(config.Options{Watch: false})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	_, err = newFromConfig(params{
		Config: cfg,
		Logger: slog.New(slog.DiscardHandler),
		LC:     fxtest.NewLifecycle(t),
	})
	if err == nil {
		t.Fatal("expected error for missing project_id, got nil")
	}
}

// TestClientFromPubsub_DefaultLogger confirms the test/advanced-use
// constructor never leaves a nil logger, mirroring kafka.ClientFromKgo.
func TestClientFromPubsub_DefaultLogger(t *testing.T) {
	t.Parallel()

	c := ClientFromPubsub(nil)
	if c.logger == nil {
		t.Fatal("expected non-nil default logger")
	}
	if c.pubs == nil {
		t.Fatal("expected initialized publisher cache")
	}
}

// TestBoundedWait_ReturnsUnderlyingError is the positive case: when fn
// finishes before ctx is done, boundedWait returns exactly fn's result.
func TestBoundedWait_ReturnsUnderlyingError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	err := boundedWait(context.Background(), func() error { return wantErr })
	if !errors.Is(err, wantErr) {
		t.Errorf("boundedWait = %v, want %v", err, wantErr)
	}

	if err := boundedWait(context.Background(), func() error { return nil }); err != nil {
		t.Errorf("boundedWait with a succeeding fn = %v, want nil", err)
	}
}

// TestBoundedWait_HonoursDeadline is the negative/boundary case this finding
// is about: OnStop's real bug was discarding its context entirely (`func(_
// context.Context) error`), so a Close that hung would hang Stop forever
// too, regardless of any deadline the caller (fx, bounded by StopTimeout)
// had set. boundedWait must return promptly once ctx is done even while fn
// is still running.
func TestBoundedWait_HonoursDeadline(t *testing.T) {
	t.Parallel()

	block := make(chan struct{})
	t.Cleanup(func() { close(block) }) // let the leftover goroutine finish

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := boundedWait(ctx, func() error {
		<-block // simulate Close hanging (e.g. an unreachable broker)
		return nil
	})
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("boundedWait = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > time.Second {
		t.Errorf("boundedWait took %v, want it to return promptly at the ~20ms deadline", elapsed)
	}
}
