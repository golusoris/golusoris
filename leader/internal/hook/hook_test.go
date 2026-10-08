// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hook_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/leader"
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

func TestIdentity_explicit(t *testing.T) {
	t.Parallel()
	require.Equal(t, "pod-1", hook.Identity("pod-1"))
}

func TestIdentity_fallsBackWhenEmpty(t *testing.T) {
	t.Parallel()
	want, err := os.Hostname()
	if err != nil {
		want = "unknown"
	}
	require.Equal(t, want, hook.Identity(""))
}

func TestLead_invokesCallbacksInOrderThenBlocksUntilDone(t *testing.T) {
	t.Parallel()
	var order []string
	leaderCtxDone := make(chan struct{})
	cb := leader.Callbacks{
		OnNewLeader: func(id string) { order = append(order, "new:"+id) },
		OnStartedLeading: func(lc context.Context) {
			order = append(order, "started")
			go func() {
				<-lc.Done()
				close(leaderCtxDone)
			}()
		},
		OnStoppedLeading: func() { order = append(order, "stopped") },
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already done: Lead must return instead of blocking.

	hook.Lead(ctx, "id-1", cb)

	require.Equal(t, []string{"new:id-1", "started", "stopped"}, order)
	select {
	case <-leaderCtxDone:
	case <-time.After(time.Second):
		t.Error("the term context must be canceled once Lead returns")
	}
}

func TestLead_nilCallbacksAreSafe(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	hook.Lead(ctx, "id-1", leader.Callbacks{})
}

func TestConfigPath(t *testing.T) {
	t.Parallel()
	require.Equal(t, "leader.elections.gc", hook.ConfigPath("gc"))
}

func TestNamedStatus_providesTaggedStatus(t *testing.T) {
	t.Parallel()
	status, tag, err := hook.NamedStatus("scheduler")
	require.NoError(t, err)
	require.Equal(t, `name:"scheduler"`, tag)
	var got struct {
		fx.In
		Status *leader.Status `name:"scheduler"`
	}
	app := fxtest.New(t, fx.NopLogger, status, fx.Populate(&got))
	defer app.RequireStart().RequireStop()
	require.NotNil(t, got.Status)
	require.False(t, got.Status.IsLeader())
}

func TestNamedStatus_validatesKey(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"a", "gc2", "a" + strings.Repeat("b", 62)} {
		_, _, err := hook.NamedStatus(key)
		require.NoError(t, err, key)
	}
	for _, key := range []string{"", "2gc", "Gc", "gc-sweep", "gc_sweep", "gc.sweep", `x"y`, "a" + strings.Repeat("b", 63)} {
		_, _, err := hook.NamedStatus(key)
		require.ErrorIs(t, err, hook.ErrInvalidKey, key)
	}
}
