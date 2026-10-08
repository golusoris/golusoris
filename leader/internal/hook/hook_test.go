// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hook_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/leader/internal/hook"
)

func TestRunUntilStop_cancelEndsRun(t *testing.T) {
	t.Parallel()
	lc := fxtest.NewLifecycle(t)
	started := make(chan struct{})
	hook.RunUntilStop(lc, slog.New(slog.DiscardHandler), "leader/test", func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return nil
	})
	lc.RequireStart()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := lc.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestRunUntilStop_wedgedRunHonorsStopDeadline(t *testing.T) {
	t.Parallel()
	lc := fxtest.NewLifecycle(t)
	release := make(chan struct{})
	defer close(release)
	hook.RunUntilStop(lc, slog.New(slog.DiscardHandler), "leader/test", func(context.Context) error {
		<-release // ignores cancellation, like a wedged backend
		return nil
	})
	lc.RequireStart()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := lc.Stop(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop error = %v, want context.DeadlineExceeded", err)
	}
	if !strings.Contains(err.Error(), "leader/test: elector did not stop") {
		t.Fatalf("Stop error %q does not name the elector", err)
	}
}

func TestRunUntilStop_runErrorIsLogged(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	lc := fxtest.NewLifecycle(t)
	hook.RunUntilStop(lc, slog.New(slog.NewTextHandler(&buf, nil)), "leader/test", func(context.Context) error {
		return errors.New("lease lost")
	})
	lc.RequireStart()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := lc.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "leader/test: run failed") || !strings.Contains(got, "lease lost") {
		t.Fatalf("log = %q, want run failure with cause", got)
	}
}
