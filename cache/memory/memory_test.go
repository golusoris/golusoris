package memory_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/cache/memory"
	"github.com/golusoris/golusoris/config"
)

func TestTypedCacheSetGet(t *testing.T) {
	t.Parallel()
	c, err := memory.NewForTest(100, 0)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	tc := memory.Typed[string, int](c, "num")
	tc.Set("a", 42)

	v, ok := tc.Get("a")
	if !ok {
		t.Fatal("expected hit")
	}
	if v != 42 {
		t.Errorf("got %d, want 42", v)
	}
}

func TestTypedCacheDelete(t *testing.T) {
	t.Parallel()
	c, err := memory.NewForTest(100, 0)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	tc := memory.Typed[string, string](c, "s")
	tc.Set("k", "v")
	tc.Delete("k")

	if _, ok := tc.Get("k"); ok {
		t.Error("expected miss after delete")
	}
}

func TestTypedCacheMiss(t *testing.T) {
	t.Parallel()
	c, err := memory.NewForTest(100, 0)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	tc := memory.Typed[string, int](c, "x")
	if _, ok := tc.Get("missing"); ok {
		t.Error("expected miss")
	}
}

func TestPrefixIsolation(t *testing.T) {
	t.Parallel()
	c, err := memory.NewForTest(100, 0)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	a := memory.Typed[string, int](c, "ns-a")
	b := memory.Typed[string, int](c, "ns-b")
	a.Set("k", 1)
	b.Set("k", 2)

	va, _ := a.Get("k")
	vb, _ := b.Get("k")
	if va != 1 || vb != 2 {
		t.Errorf("prefix isolation broken: a=%d b=%d", va, vb)
	}
}

// TestModule_StartsAndStops boots the memory Module via fxtest to cover
// defaultOptions, loadOptions, and newCache.
func TestModule_StartsAndStops(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	app := fxtest.New(t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		memory.Module,
		fx.Invoke(func(*memory.Cache) {}),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := app.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
