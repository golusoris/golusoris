package fxtest_test

import (
	"context"
	"testing"

	"go.uber.org/fx"

	gfxtest "github.com/golusoris/golusoris/testutil/fxtest"
)

func TestNew_startsAndStops(t *testing.T) {
	started := false

	gfxtest.New(t,
		fx.Invoke(func(lc fx.Lifecycle) {
			lc.Append(fx.Hook{
				OnStart: func(_ context.Context) error {
					started = true
					return nil
				},
			})
		}),
	)

	if !started {
		t.Fatal("OnStart was not called")
	}
}

func TestPopulate(t *testing.T) {
	type Dep struct{ Value string }

	var dep *Dep
	gfxtest.New(t,
		fx.Provide(func() *Dep { return &Dep{Value: "hello"} }),
		gfxtest.Populate(&dep),
	)

	if dep == nil {
		t.Fatal("dep was not populated")
	}
	if dep.Value != "hello" {
		t.Fatalf("dep.Value = %q, want %q", dep.Value, "hello")
	}
}
