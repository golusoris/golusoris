// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Ported from SnatchArr api/internal/leaderx at 6111de5a.

package gate_test

import (
	"context"
	"testing"
	"time"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/bootstrap"
	"github.com/golusoris/golusoris/leader/gate"
)

// Positive: Module wires gate and starts loops when leader election is disabled.
func TestModule_DisabledRunsWithProcess(t *testing.T) {
	t.Parallel()
	loop := newFakeLoop(nil)
	app := fx.New(
		bootstrap.Core,
		gate.Module,
		fx.Provide(func() *fakeLoop { return loop }, gate.Provide[*fakeLoop]()),
	)

	startCtx, startCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer startCancel()
	if err := app.Start(startCtx); err != nil {
		t.Fatalf("app.Start: %v", err)
	}

	await(t, loop.started, "loop start on app start")

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopCancel()
	if err := app.Stop(stopCtx); err != nil {
		t.Fatalf("app.Stop: %v", err)
	}

	if starts, stops := loop.counts(); starts != 1 || stops != 1 {
		t.Fatalf("starts=%d stops=%d, want 1/1", starts, stops)
	}
}

// Positive: DefaultOptions has Enabled=false.
func TestDefaultOptions(t *testing.T) {
	t.Parallel()
	opts := gate.DefaultOptions()
	if opts.Enabled {
		t.Fatal("expected DefaultOptions().Enabled to be false")
	}
	if opts.PG.Retry != 2*time.Second {
		t.Fatalf("expected PG.Retry to be 2s, got %v", opts.PG.Retry)
	}
}
