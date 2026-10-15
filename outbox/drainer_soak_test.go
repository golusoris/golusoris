// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build soak

package outbox_test

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/riverqueue/river"

	"github.com/golusoris/golusoris/jobs"
	"github.com/golusoris/golusoris/outbox"
	"github.com/golusoris/golusoris/testutil/load"
	rivertest "github.com/golusoris/golusoris/testutil/river"
)

type soakOutboxArgs struct {
	ID int64 `json:"id"`
}

func (soakOutboxArgs) Kind() string { return "soak_outbox_job" }

type soakOutboxWorker struct {
	river.WorkerDefaults[soakOutboxArgs]
	processed atomic.Int64
}

func (w *soakOutboxWorker) Work(_ context.Context, _ *river.Job[soakOutboxArgs]) error {
	w.processed.Add(1)
	return nil
}

func TestOutboxDrainer_Soak(t *testing.T) {
	t.Parallel()
	w := &soakOutboxWorker{}
	rv := rivertest.Start(t, rivertest.Options{
		Register: func(workers *jobs.Workers) {
			if err := jobs.Register(workers, w); err != nil {
				t.Fatalf("Register: %v", err)
			}
		},
	})
	applyOutboxMigration(t, rv.Pool.Config().ConnConfig.ConnString())

	dispatcher := func(_ context.Context, ev outbox.Event) (river.JobArgs, *river.InsertOpts, error) {
		return soakOutboxArgs{ID: ev.ID}, nil, nil
	}
	drainer := outbox.NewDrainer(
		rv.Pool, rv.Client, dispatcher,
		slog.New(slog.DiscardHandler), clockwork.NewRealClock(),
		outbox.DrainerOptions{
			Enabled:  true,
			Interval: 50 * time.Millisecond,
			Batch:    25,
		},
	)
	drainerCtx, drainerCancel := context.WithCancel(context.Background())
	t.Cleanup(drainerCancel)
	go func() {
		_ = drainer.Run(drainerCtx)
	}()

	var counter atomic.Int64
	op := func(ctx context.Context) error {
		id := counter.Add(1)
		opCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		return insertOutboxRow(opCtx, rv, id)
	}

	load.RunSoak(t, load.SoakOptions{
		Name:        "outbox/drainer",
		Pool:        rv.Pool,
		Pace:        15 * time.Millisecond,
		Concurrency: 2,
	}, op)
}

func insertOutboxRow(ctx context.Context, rv *rivertest.Harness, id int64) error {
	tx, err := rv.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("outbox soak begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := outbox.Add(ctx, tx, "soak_outbox_job", soakOutboxArgs{ID: id}); err != nil {
		return fmt.Errorf("outbox soak add: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("outbox soak commit: %w", err)
	}
	return nil
}
