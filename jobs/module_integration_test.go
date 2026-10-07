// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jobs_test

import (
	"context"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/jobs"
	rivertest "github.com/golusoris/golusoris/testutil/river"
)

type moduleArgs struct{}

func (moduleArgs) Kind() string { return "module-probe" }

type moduleWorker struct {
	jobs.WorkerDefaults[moduleArgs]
	ran chan struct{}
}

func (w *moduleWorker) Work(context.Context, *jobs.Job[moduleArgs]) error {
	w.ran <- struct{}{}
	return nil
}

// TestModule_keepsWorkingAfterStartContextEnds is the regression for River
// hard-stopping when fx cancels its start context: fxtest cancels it as soon
// as RequireStart returns, so a job inserted afterwards must still be worked.
func TestModule_keepsWorkingAfterStartContextEnds(t *testing.T) {
	t.Parallel()
	rv := rivertest.Start(t, rivertest.Options{})
	cfg, err := config.New(config.Options{EnvPrefix: "JOBS_MODULE_TEST_"})
	if err != nil {
		t.Fatal(err)
	}
	w := &moduleWorker{ran: make(chan struct{}, 1)}
	var client *jobs.Client
	app := fxtest.New(t,
		fx.Supply(rv.Pool, cfg, discard()),
		jobs.Module,
		fx.Invoke(func(ws *jobs.Workers) error { return jobs.Register(ws, w) }),
		fx.Populate(&client),
	)
	app.RequireStart()
	defer app.RequireStop()
	select {
	case <-client.Stopped():
		t.Fatal("River stopped when fx ended the start context")
	case <-time.After(time.Second):
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := client.Insert(ctx, moduleArgs{}, nil); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	select {
	case <-w.ran:
	case <-ctx.Done():
		t.Fatal("job inserted after fx start was never worked; River stopped with the start context")
	}
}
