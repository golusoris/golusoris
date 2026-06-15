package clikit_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/clikit"
)

// TestWithFxRunPropagatesError is the #251 core fix: a one-shot command's error
// must reach the cobra exit path (not be swallowed by app.Run()).
func TestWithFxRunPropagatesError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("boom")
	cmd := clikit.Command("oneshot", "x", clikit.WithFxRun(
		func(context.Context) error { return wantErr },
	))
	if err := cmd.Execute(); !errors.Is(err, wantErr) {
		t.Fatalf("Execute err = %v, want %v", err, wantErr)
	}
}

// TestWithFxRunSucceedsWithoutBlocking verifies the success path runs to
// completion (no app.Run()/signal wait).
func TestWithFxRunSucceedsWithoutBlocking(t *testing.T) {
	t.Parallel()
	ran := false
	cmd := clikit.Command("oneshot", "x", clikit.WithFxRun(
		func(context.Context) error { ran = true; return nil },
	))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !ran {
		t.Error("run func was not called")
	}
}

// TestWithFxRunPopulatesDeps verifies the documented fx.Populate pattern works.
func TestWithFxRunPopulatesDeps(t *testing.T) {
	t.Parallel()
	var got string
	cmd := clikit.Command("oneshot", "x", clikit.WithFxRun(
		func(context.Context) error {
			if got != "hello" {
				return fmt.Errorf("dep not populated: %q", got)
			}
			return nil
		},
		fx.Provide(func() string { return "hello" }),
		fx.Populate(&got),
	))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
}
