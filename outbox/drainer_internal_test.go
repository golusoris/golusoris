// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package outbox

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/riverqueue/river"

	dbmigrate "github.com/golusoris/golusoris/db/migrate"
	dbpgx "github.com/golusoris/golusoris/db/pgx"
	"github.com/golusoris/golusoris/jobs"
	rivertest "github.com/golusoris/golusoris/testutil/river"
)

type atomicOrderArgs struct {
	OrderID string `json:"order_id"`
}

func (atomicOrderArgs) Kind() string { return "order.created" }

type atomicOrderWorker struct {
	river.WorkerDefaults[atomicOrderArgs]
}

func (*atomicOrderWorker) Work(context.Context, *river.Job[atomicOrderArgs]) error { return nil }

type rejectedInsertArgs struct{}

func (rejectedInsertArgs) Kind() string { return "rejected.insert" }

func TestNewDrainerHandlesMissingDispatcherAndTypedNilClock(t *testing.T) {
	t.Parallel()

	t.Run("dispatcher", func(t *testing.T) {
		t.Parallel()
		drainer := NewDrainer(nil, nil, nil, nil, clockwork.NewRealClock(), DrainerOptions{})
		if err := drainer.dispatchOne(t.Context(), nil, Event{}); err == nil {
			t.Fatal("missing dispatcher should return an error")
		}
	})

	t.Run("clock", func(t *testing.T) {
		t.Parallel()
		var clk *clockwork.FakeClock
		drainer := NewDrainer(nil, nil, func(context.Context, Event) (river.JobArgs, *river.InsertOpts, error) {
			return nil, nil, nil
		}, nil, clk, DrainerOptions{})
		if drainer.clk.Now().IsZero() {
			t.Fatal("typed-nil clock did not use the default")
		}
	})

	t.Run("pool", func(t *testing.T) {
		t.Parallel()
		drainer := NewDrainer(nil, nil, func(context.Context, Event) (river.JobArgs, *river.InsertOpts, error) {
			return nil, nil, nil
		}, nil, clockwork.NewRealClock(), DrainerOptions{})
		if err := drainer.drain(t.Context()); err == nil {
			t.Fatal("missing pool should return an error")
		}
	})

	t.Run("client", func(t *testing.T) {
		t.Parallel()
		drainer := NewDrainer(nil, nil, func(context.Context, Event) (river.JobArgs, *river.InsertOpts, error) {
			return atomicOrderArgs{}, nil, nil
		}, nil, clockwork.NewRealClock(), DrainerOptions{})
		if err := drainer.dispatchOne(t.Context(), nil, Event{}); err == nil {
			t.Fatal("missing jobs client should return an error")
		}
	})
}

func TestConcurrentDrainersInsertOneRiverJob(t *testing.T) {
	t.Parallel()
	rv := rivertest.Start(t, rivertest.Options{
		Register: func(workers *jobs.Workers) {
			if err := jobs.Register(workers, &atomicOrderWorker{}); err != nil {
				t.Fatalf("Register: %v", err)
			}
		},
	})
	applyInternalOutboxMigration(t, rv.Pool.Config().ConnConfig.ConnString())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, beginErr := rv.Pool.Begin(ctx)
	if beginErr != nil {
		t.Fatalf("begin: %v", beginErr)
	}
	if err := Add(ctx, tx, "order.created", atomicOrderArgs{OrderID: "O-atomic"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	dispatcher := func(_ context.Context, ev Event) (river.JobArgs, *river.InsertOpts, error) {
		var args atomicOrderArgs
		if err := Unmarshal(ev, &args); err != nil {
			return nil, nil, err
		}
		return args, nil, nil
	}
	newDrainer := func() *Drainer {
		return NewDrainer(
			rv.Pool, rv.Client, dispatcher,
			slog.New(slog.DiscardHandler), clockwork.NewRealClock(),
			DrainerOptions{Interval: time.Hour, Batch: 10},
		)
	}

	start := make(chan struct{})
	done := make(chan error, 2)
	for range 2 {
		drainer := newDrainer()
		go func() {
			<-start
			done <- drainer.drain(ctx)
		}()
	}
	close(start)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatalf("drain: %v", err)
		}
	}

	var jobsCount int
	if err := rv.Pool.QueryRow(
		ctx,
		`SELECT count(*) FROM river_job WHERE kind = $1`, "order.created",
	).Scan(&jobsCount); err != nil {
		t.Fatalf("count river jobs: %v", err)
	}
	if jobsCount != 1 {
		var totalJobs int
		if err := rv.Pool.QueryRow(ctx, `SELECT count(*) FROM river_job`).Scan(&totalJobs); err != nil {
			t.Fatalf("count all river jobs: %v", err)
		}
		var dispatchedAt *time.Time
		var attempts int
		var lastError string
		if err := rv.Pool.QueryRow(
			ctx,
			`SELECT dispatched_at, attempts, coalesce(last_error, '') FROM golusoris_outbox LIMIT 1`,
		).Scan(&dispatchedAt, &attempts, &lastError); err != nil {
			t.Fatalf("read outbox diagnostics: %v", err)
		}
		t.Fatalf(
			"river jobs kind=%d total=%d, want 1; dispatched_at=%v attempts=%d last_error=%v",
			jobsCount, totalJobs, dispatchedAt, attempts, lastError,
		)
	}
	events, pendingErr := Pending(ctx, rv.Pool, 10)
	if pendingErr != nil {
		t.Fatalf("Pending: %v", pendingErr)
	}
	if len(events) != 0 {
		t.Fatalf("pending events = %d, want 0", len(events))
	}
}

func TestPoisonEventDoesNotStarveLaterEvent(t *testing.T) {
	t.Parallel()
	rv := rivertest.Start(t, rivertest.Options{})
	applyInternalOutboxMigration(t, rv.Pool.Config().ConnConfig.ConnString())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, err := rv.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err = Add(ctx, tx, "poison", map[string]int{"id": 1}); err != nil {
		t.Fatalf("add poison: %v", err)
	}
	if err = Add(ctx, tx, "valid", map[string]int{"id": 2}); err != nil {
		t.Fatalf("add valid: %v", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	dispatcher := func(_ context.Context, event Event) (river.JobArgs, *river.InsertOpts, error) {
		if event.Kind == "poison" {
			return nil, nil, errors.New("invalid payload")
		}
		return nil, nil, nil
	}
	drainer := NewDrainer(
		rv.Pool, rv.Client, dispatcher,
		slog.New(slog.DiscardHandler), clockwork.NewRealClock(),
		DrainerOptions{Interval: time.Hour, Batch: 1},
	)
	if err = drainer.drain(ctx); err != nil {
		t.Fatalf("first drain: %v", err)
	}
	if err = drainer.drain(ctx); err != nil {
		t.Fatalf("second drain: %v", err)
	}

	var attempts int
	var stillPending bool
	var delayed bool
	if err = rv.Pool.QueryRow(ctx, `
		SELECT attempts, dispatched_at IS NULL, next_attempt_at > now()
		FROM golusoris_outbox
		WHERE kind = 'poison'
	`).Scan(&attempts, &stillPending, &delayed); err != nil {
		t.Fatalf("read poison event: %v", err)
	}
	if attempts != 1 || !stillPending || !delayed {
		t.Fatalf(
			"poison attempts=%d pending=%t delayed=%t; want 1,true,true",
			attempts,
			stillPending,
			delayed,
		)
	}
	var validDispatched bool
	if err = rv.Pool.QueryRow(ctx, `
		SELECT dispatched_at IS NOT NULL FROM golusoris_outbox WHERE kind = 'valid'
	`).Scan(&validDispatched); err != nil {
		t.Fatalf("read valid event: %v", err)
	}
	if !validDispatched {
		t.Fatal("valid event remained pending behind poison event")
	}
}

func TestRetryScheduleExcludesFutureAndPrioritizesDueFailure(t *testing.T) {
	t.Parallel()
	rv := rivertest.Start(t, rivertest.Options{})
	applyInternalOutboxMigration(t, rv.Pool.Config().ConnConfig.ConnString())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var retryID int64
	if err := rv.Pool.QueryRow(ctx, `
		INSERT INTO golusoris_outbox (kind, payload)
		VALUES ('retry', '{}'::jsonb)
		RETURNING id
	`).Scan(&retryID); err != nil {
		t.Fatalf("insert retry event: %v", err)
	}
	if err := markFailed(ctx, rv.Pool, retryID, errors.New("temporary"), time.Hour); err != nil {
		t.Fatalf("schedule retry: %v", err)
	}

	var freshID int64
	if err := rv.Pool.QueryRow(ctx, `
		INSERT INTO golusoris_outbox (kind, payload)
		VALUES ('fresh-before-due', '{}'::jsonb)
		RETURNING id
	`).Scan(&freshID); err != nil {
		t.Fatalf("insert fresh event: %v", err)
	}
	events, err := Pending(ctx, rv.Pool, 10)
	if err != nil {
		t.Fatalf("Pending before due: %v", err)
	}
	if len(events) != 1 || events[0].ID != freshID {
		t.Fatalf("pending before due = %+v, want only fresh event %d", events, freshID)
	}
	if err = MarkDispatched(ctx, rv.Pool, freshID); err != nil {
		t.Fatalf("mark fresh dispatched: %v", err)
	}

	if _, err = rv.Pool.Exec(
		ctx, `UPDATE golusoris_outbox SET next_attempt_at = now() WHERE id = $1`, retryID,
	); err != nil {
		t.Fatalf("make retry due: %v", err)
	}
	for range 5 {
		if _, err = rv.Pool.Exec(ctx, `
			INSERT INTO golusoris_outbox (kind, payload)
			VALUES ('continuous-fresh', '{}'::jsonb)
		`); err != nil {
			t.Fatalf("insert continuous fresh event: %v", err)
		}
	}
	events, err = Pending(ctx, rv.Pool, 1)
	if err != nil {
		t.Fatalf("Pending at due boundary: %v", err)
	}
	if len(events) != 1 || events[0].ID != retryID || events[0].Attempts != 1 {
		t.Fatalf("first due event = %+v, want retry %d with one attempt", events, retryID)
	}
}

func TestRiverInsertFailureRecordsAttemptAfterSavepointRollback(t *testing.T) {
	t.Parallel()
	rv := rivertest.Start(t, rivertest.Options{})
	applyInternalOutboxMigration(t, rv.Pool.Config().ConnConfig.ConnString())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := rv.Pool.Exec(
		ctx,
		`ALTER TABLE river_job ADD CONSTRAINT reject_insert_test CHECK (kind <> 'rejected.insert')`,
	); err != nil {
		t.Fatalf("add rejecting constraint: %v", err)
	}
	tx, err := rv.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err = Add(ctx, tx, "rejected.insert", map[string]int{"id": 1}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	drainer := NewDrainer(
		rv.Pool,
		rv.Client,
		func(context.Context, Event) (river.JobArgs, *river.InsertOpts, error) {
			return rejectedInsertArgs{}, nil, nil
		},
		slog.New(slog.DiscardHandler),
		clockwork.NewRealClock(),
		DrainerOptions{Interval: time.Hour, Batch: 1},
	)
	if err = drainer.drain(ctx); err != nil {
		t.Fatalf("drain: %v", err)
	}

	var attempts int
	var lastError string
	if err = rv.Pool.QueryRow(
		ctx,
		`SELECT attempts, coalesce(last_error, '') FROM golusoris_outbox LIMIT 1`,
	).Scan(&attempts, &lastError); err != nil {
		t.Fatalf("read outbox failure: %v", err)
	}
	if attempts != 1 || lastError == "" {
		t.Fatalf("attempts = %d, last_error = %q; want 1 and recorded error", attempts, lastError)
	}
}

func applyInternalOutboxMigration(t *testing.T, dsn string) {
	t.Helper()
	migrator, err := dbmigrate.New(
		dbmigrate.Options{Path: "migrations"}.WithFS(MigrationsFS),
		dbpgx.Options{DSN: dsn},
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	t.Cleanup(func() { _ = migrator.Close() })
	if err := migrator.Up(); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
}
