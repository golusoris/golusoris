package golusoris_test

import (
	"context"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris"
)

// TestCoreStartsAndStops asserts that golusoris.Core can be wired into an fx
// application, started, and stopped without error. This is the smoke test
// that protects against module wiring regressions.
func TestCoreStartsAndStops(t *testing.T) {
	t.Parallel()
	app := fxtest.New(t, golusoris.Core)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := app.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := app.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// ExampleCore demonstrates composing a minimal golusoris app. The fx.NopLogger
// keeps the example output clean; production apps omit it to get the
// framework's slog logger.
func ExampleCore() {
	app := fx.New(
		fx.NopLogger,
		golusoris.Core,
	)
	_ = app // app.Run() in production
	// Output:
}
