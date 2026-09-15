// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ginkgofx

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/fx"
)

// TestStartApp_positive_stopsWithinTimeout covers a well-behaved OnStart
// hook completing before the deadline.
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
	if !started {
		t.Fatal("OnStart was not called")
	}
	if err := StopApp(context.Background(), app, time.Second); err != nil {
		t.Fatalf("StopApp: %v", err)
	}
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
