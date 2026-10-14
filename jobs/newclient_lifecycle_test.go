// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jobs_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/jobs"
	pgtest "github.com/golusoris/golusoris/testutil/pg"
)

func TestNewClient_InvalidConfigAndNilArgs(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	driver := riverpgxv5.New(nil)

	// Negative: nil logger
	_, err := jobs.NewClient(driver, jobs.DefaultOptions(), nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil logger")

	// Negative: nil driver
	_, err = jobs.NewClient[pgx.Tx](nil, jobs.DefaultOptions(), nil, logger)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil driver")

	// Negative: invalid options (negative stop duration)
	invalidOpts := jobs.Options{
		Stop: jobs.StopOptions{Soft: -1},
	}
	_, err = jobs.NewClient(driver, invalidOpts, nil, logger)
	require.Error(t, err)

	// Positive without pool (transactional client)
	client, err := jobs.NewClient(driver, jobs.DefaultOptions(), nil, logger)
	require.NoError(t, err)
	require.NotNil(t, client)
}

type testLifecycleArgs struct{}

func (testLifecycleArgs) Kind() string { return "test-lifecycle-kind" }

type testLifecycleWorker struct {
	river.WorkerDefaults[testLifecycleArgs]
	ran chan struct{}
}

func (w *testLifecycleWorker) Work(context.Context, *river.Job[testLifecycleArgs]) error {
	w.ran <- struct{}{}
	return nil
}

func TestNewClient_AppendLifecycle(t *testing.T) {
	t.Parallel()
	pool := pgtest.Start(t)

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	require.NoError(t, jobs.Migrate(ctx, pool))

	logger := slog.New(slog.DiscardHandler)
	workers := jobs.NewWorkers()
	w := &testLifecycleWorker{ran: make(chan struct{}, 1)}
	require.NoError(t, jobs.Register[testLifecycleArgs](workers, w))

	opts := jobs.DefaultOptions()
	client, err := jobs.NewClient(riverpgxv5.New(pool), opts, workers, logger)
	require.NoError(t, err)

	lifecycle := fxtest.NewLifecycle(t)
	jobs.AppendLifecycle(lifecycle, client, opts, logger)
	lifecycle.RequireStart()

	// OnStart runs River past the start hook: a job inserted afterwards is worked.
	_, err = client.Insert(ctx, testLifecycleArgs{}, nil)
	require.NoError(t, err)
	select {
	case <-w.ran:
	case <-ctx.Done():
		t.Fatal("job inserted after AppendLifecycle start was never worked")
	}

	// OnStop drains the client.
	lifecycle.RequireStop()
	select {
	case <-client.Stopped():
	case <-ctx.Done():
		t.Fatal("client still running after AppendLifecycle stop")
	}
}
