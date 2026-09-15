// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package nri

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

// blockingStub is a [nristub.Stub] whose Run blocks on release regardless of
// ctx cancellation — it models a real plugin's Run loop that does not return
// the instant its context is cancelled (e.g. it is blocked reading the ttrpc
// socket), so tests can tell the difference between "cancel requested" and
// "the Run goroutine has actually exited".
type blockingStub struct {
	fakeStub
	started chan struct{}
	release chan struct{}
}

func newBlockingStub() *blockingStub {
	return &blockingStub{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (b *blockingStub) Run(context.Context) error {
	close(b.started)
	<-b.release
	return nil
}

// noopShutdowner is an [fx.Shutdowner] test double that records whether
// Shutdown was requested, without needing a real fx.App.
type noopShutdowner struct {
	called chan struct{}
}

func newNoopShutdowner() *noopShutdowner {
	return &noopShutdowner{called: make(chan struct{})}
}

func (s *noopShutdowner) Shutdown(...fx.ShutdownOption) error {
	close(s.called)
	return nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// TestRunPluginOnStopWaitsForRunToExit is the regression test for the OnStop
// hook that used to only call cancel() and return nil immediately, without
// waiting for the Run goroutine it started on OnStart to actually finish. A
// naive implementation makes this test's first select fire the "returned too
// early" branch.
func TestRunPluginOnStopWaitsForRunToExit(t *testing.T) {
	t.Parallel()

	stub := newBlockingStub()
	reg := &Registration{Stub: stub}
	sd := newNoopShutdowner()

	lc := fxtest.NewLifecycle(t)
	runPlugin(lc, reg, discardLogger(), sd)

	if err := lc.Start(context.Background()); err != nil {
		t.Fatalf("lc.Start: %v", err)
	}
	<-stub.started

	stopErr := make(chan error, 1)
	go func() { stopErr <- lc.Stop(context.Background()) }()

	select {
	case err := <-stopErr:
		t.Fatalf("OnStop returned (err=%v) before the Run goroutine exited", err)
	case <-time.After(50 * time.Millisecond):
		// Good: OnStop is still blocked waiting for Run, exactly as it
		// should be while the stub has not released.
	}

	close(stub.release)

	select {
	case err := <-stopErr:
		if err != nil {
			t.Fatalf("lc.Stop: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("OnStop did not return after the Run goroutine exited")
	}
}

// TestRunPluginOnStopTimesOutIfRunNeverExits verifies OnStop is bounded by
// the context fx passes it, rather than blocking forever on a wedged Run.
func TestRunPluginOnStopTimesOutIfRunNeverExits(t *testing.T) {
	t.Parallel()

	stub := newBlockingStub()
	reg := &Registration{Stub: stub}
	sd := newNoopShutdowner()

	lc := fxtest.NewLifecycle(t)
	runPlugin(lc, reg, discardLogger(), sd)

	if err := lc.Start(context.Background()); err != nil {
		t.Fatalf("lc.Start: %v", err)
	}
	<-stub.started
	// Deliberately never close stub.release — Run never returns.

	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := lc.Stop(stopCtx)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lc.Stop error = %v, want wrapping context.DeadlineExceeded", err)
	}
	if elapsed >= 500*time.Millisecond {
		t.Fatalf("lc.Stop took %s, want well under a second (bounded by the 30ms OnStop context)", elapsed)
	}
}
