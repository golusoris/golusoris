// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ginkgofx

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"go.uber.org/fx"
)

// cleanupHelperEnv gates TestStartApp_cleanup_helperFatals: set only in the
// subprocess TestStartApp_cleanup_stopsAppDespiteMidTestFatal re-execs via
// RunHelperProcess.
const cleanupHelperEnv = "GINKGOFX_CLEANUP_HELPER"

// cleanupHelperStoppedMarker is the exact line
// TestStartApp_cleanup_helperFatals logs once its t.Cleanup-registered
// StopApp call has run; TestStartApp_cleanup_stopsAppDespiteMidTestFatal
// greps the helper's captured output for it.
const cleanupHelperStoppedMarker = "cleanupHelper: StopApp ran"

// TestStartApp_positive_stopsWithinTimeout covers a well-behaved OnStart
// hook completing before the deadline.
//
// StopApp is registered via t.Cleanup immediately after a successful
// Start, before any assertion that could t.Fatal — otherwise a failing
// assertion between Start and a sequential Stop call would abort the test
// (t.Fatal calls runtime.Goexit) and skip Stop entirely, leaking the app.
// t.Cleanup runs regardless of how the test body exits, so the app is
// always stopped. TestStartApp_cleanup_stopsAppDespiteMidTestFatal below
// proves this mechanism actually holds.
func TestStartApp_positive_startsWithinTimeout(t *testing.T) {
	t.Parallel()
	started := false
	app := fx.New(fx.Invoke(func(lc fx.Lifecycle) {
		lc.Append(fx.Hook{OnStart: func(context.Context) error {
			started = true
			return nil
		}})
	}))

	if err := StartApp(context.Background(), app, time.Second); err != nil {
		t.Fatalf("StartApp: %v", err)
	}
	t.Cleanup(func() {
		if err := StopApp(context.Background(), app, time.Second); err != nil {
			t.Errorf("StopApp: %v", err)
		}
	})

	if !started {
		t.Fatal("OnStart was not called")
	}
}

// TestStartApp_cleanup_stopsAppDespiteMidTestFatal is the regression test
// for the leak TestStartApp_positive_startsWithinTimeout used to risk: a
// mid-test t.Fatal firing after a successful StartApp must not skip
// StopApp. It re-execs TestStartApp_cleanup_helperFatals in a subprocess
// (via RunHelperProcess) rather than reproducing the Fatal inline, because
// a real t.Fatal in an ordinary subtest here would mark this package's own
// `go test` run as failed — exactly the outcome a regression test must not
// cause. The helper deliberately fails after a successful Start; this test
// asserts (a) it really did fail (sanity: we're exercising the right
// path) and (b) its t.Cleanup-registered StopApp still logged
// cleanupHelperStoppedMarker before exiting. Had Stop instead been
// sequenced as a plain statement after other assertions (the old shape),
// the helper's Fatal would end it via runtime.Goexit before ever reaching
// that statement, and the marker would be absent.
func TestStartApp_cleanup_stopsAppDespiteMidTestFatal(t *testing.T) {
	t.Parallel()

	out, err := RunHelperProcess(t, "TestStartApp_cleanup_helperFatals", cleanupHelperEnv)
	if err == nil {
		t.Fatalf("helper process unexpectedly succeeded (want its simulated Fatal to fail it); output:\n%s", out)
	}
	if !strings.Contains(string(out), cleanupHelperStoppedMarker) {
		t.Fatalf("helper output missing %q (StopApp did not run via t.Cleanup after its mid-test Fatal); output:\n%s", cleanupHelperStoppedMarker, out)
	}
}

// TestStartApp_cleanup_helperFatals is not a real test: it only runs when
// re-exec'd by TestStartApp_cleanup_stopsAppDespiteMidTestFatal (via
// cleanupHelperEnv). It reproduces
// TestStartApp_positive_startsWithinTimeout's shape — StartApp, then
// t.Cleanup(StopApp) registered immediately, then an assertion that can
// fail the test — but forces the failing branch, logging
// cleanupHelperStoppedMarker from inside the cleanup so the parent process
// can confirm StopApp ran despite the Fatal below it.
func TestStartApp_cleanup_helperFatals(t *testing.T) {
	t.Parallel()
	if os.Getenv(cleanupHelperEnv) == "" {
		t.Skip("only runs as a re-exec'd child of TestStartApp_cleanup_stopsAppDespiteMidTestFatal")
	}

	app := fx.New(fx.Invoke(func(lc fx.Lifecycle) {
		lc.Append(fx.Hook{OnStop: func(context.Context) error {
			return nil
		}})
	}))

	if err := StartApp(context.Background(), app, time.Second); err != nil {
		t.Fatalf("StartApp: %v", err)
	}
	t.Cleanup(func() {
		if err := StopApp(context.Background(), app, time.Second); err != nil {
			t.Errorf("StopApp: %v", err)
			return
		}
		t.Log(cleanupHelperStoppedMarker)
	})

	t.Fatal("simulated mid-test failure after a successful StartApp")
}

// TestStartApp_negative_propagatesHookError covers a hook that fails
// immediately: the error must surface from StartApp, not be swallowed.
func TestStartApp_negative_propagatesHookError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("boom")
	app := fx.New(fx.Invoke(func(lc fx.Lifecycle) {
		lc.Append(fx.Hook{OnStart: func(context.Context) error {
			return wantErr
		}})
	}))

	err := StartApp(context.Background(), app, time.Second)
	if err == nil {
		t.Fatal("StartApp: got nil error, want wantErr")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("StartApp error = %v, want wrapping %v", err, wantErr)
	}
}

// TestStartApp_boundary_deadlineExceeded covers a hook that outlives the
// configured timeout: StartApp must return once the context deadline
// passes rather than block until the hook itself returns.
func TestStartApp_boundary_deadlineExceeded(t *testing.T) {
	t.Parallel()
	const timeout = 20 * time.Millisecond
	app := fx.New(fx.Invoke(func(lc fx.Lifecycle) {
		lc.Append(fx.Hook{OnStart: func(ctx context.Context) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
				return nil
			}
		}})
	}))

	begin := time.Now()
	err := StartApp(context.Background(), app, timeout)
	elapsed := time.Since(begin)

	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("StartApp error = %v, want wrapping context.DeadlineExceeded", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("StartApp took %v, want well under the 5s hook sleep", elapsed)
	}
}

// TestStopApp_negative_propagatesHookError mirrors the Start case for the
// symmetric Stop path.
func TestStopApp_negative_propagatesHookError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("stop boom")
	app := fx.New(fx.Invoke(func(lc fx.Lifecycle) {
		lc.Append(fx.Hook{OnStop: func(context.Context) error {
			return wantErr
		}})
	}))
	if err := StartApp(context.Background(), app, time.Second); err != nil {
		t.Fatalf("StartApp: %v", err)
	}

	err := StopApp(context.Background(), app, time.Second)
	if err == nil || !errors.Is(err, wantErr) {
		t.Fatalf("StopApp error = %v, want wrapping %v", err, wantErr)
	}
}

// TestOptions_withDefaults_boundary covers the zero-value Options
// boundary: both fields must fall back to the package defaults.
func TestOptions_withDefaults_boundary(t *testing.T) {
	t.Parallel()
	got := Options{}.withDefaults()
	if got.StartTimeout != DefaultStartTimeout {
		t.Errorf("StartTimeout = %v, want %v", got.StartTimeout, DefaultStartTimeout)
	}
	if got.StopTimeout != DefaultStopTimeout {
		t.Errorf("StopTimeout = %v, want %v", got.StopTimeout, DefaultStopTimeout)
	}

	explicit := Options{StartTimeout: time.Minute, StopTimeout: 2 * time.Minute}.withDefaults()
	if explicit.StartTimeout != time.Minute {
		t.Errorf("StartTimeout = %v, want unchanged %v", explicit.StartTimeout, time.Minute)
	}
	if explicit.StopTimeout != 2*time.Minute {
		t.Errorf("StopTimeout = %v, want unchanged %v", explicit.StopTimeout, 2*time.Minute)
	}
}
