package audit_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/audit"
	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/config"
)

// TestModule_ProvidesLogger boots the audit Module via fxtest to cover
// loadOptions defaults, the default MemoryStore provider, and newLogger.
func TestModule_ProvidesLogger(t *testing.T) {
	t.Parallel()
	cfg, cfgErr := config.New(config.Options{})
	if cfgErr != nil {
		t.Fatalf("config.New: %v", cfgErr)
	}

	var got *audit.Logger
	app := fxtest.New(t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		clock.Module,
		audit.Module,
		fx.Populate(&got),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if startErr := app.Start(ctx); startErr != nil {
		t.Fatalf("Start: %v", startErr)
	}
	defer func() {
		if stopErr := app.Stop(ctx); stopErr != nil {
			t.Fatalf("Stop: %v", stopErr)
		}
	}()

	if got == nil {
		t.Fatal("expected *audit.Logger to be provided")
	}

	// The default backend is MemoryStore: a logged event is retrievable.
	if logErr := got.Log(ctx, audit.Event{Actor: "user:1", Action: "test", Target: "t:1"}); logErr != nil {
		t.Fatalf("Log: %v", logErr)
	}
	evs, listErr := got.List(ctx, audit.Filter{Actor: "user:1"})
	if listErr != nil {
		t.Fatalf("List: %v", listErr)
	}
	if len(evs) != 1 {
		t.Fatalf("expected 1 event from default MemoryStore, got %d", len(evs))
	}
}
