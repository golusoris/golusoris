package outbox_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/riverqueue/river"

	dbmigrate "github.com/golusoris/golusoris/db/migrate"
	dbpgx "github.com/golusoris/golusoris/db/pgx"
	"github.com/golusoris/golusoris/jobs"
	"github.com/golusoris/golusoris/outbox"
	pgtest "github.com/golusoris/golusoris/testutil/pg"
	rivertest "github.com/golusoris/golusoris/testutil/river"
)

// orderArgs + orderWorker exercise an end-to-end outbox → river flow.
type orderArgs struct {
	OrderID string `json:"order_id"`
}

func (orderArgs) Kind() string { return "order.created" }

type orderWorker struct {
	river.WorkerDefaults[orderArgs]
	seen atomic.Int32
}

func (w *orderWorker) Work(_ context.Context, _ *river.Job[orderArgs]) error {
	w.seen.Add(1)
	return nil
}

func applyOutboxMigration(t *testing.T, dsn string) {
	t.Helper()
	m, err := dbmigrate.New(
		dbmigrate.Options{Path: "migrations"}.WithFS(outbox.MigrationsFS),
		dbpgx.Options{DSN: dsn},
		slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	defer func() { _ = m.Close() }()
	if err := m.Up(); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
}

func TestAddPendingRoundTrip(t *testing.T) {
	t.Parallel()
	pool := pgtest.Start(t)
	applyOutboxMigration(t, pool.Config().ConnConfig.ConnString())

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if addErr := outbox.Add(ctx, tx, "order.created", orderArgs{OrderID: "O-1"}); addErr != nil {
		t.Fatalf("Add: %v", addErr)
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		t.Fatalf("commit: %v", commitErr)
	}

	events, err := outbox.Pending(ctx, pool, 10)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].Kind != "order.created" {
		t.Errorf("Kind = %q", events[0].Kind)
	}

	var got orderArgs
	if err := outbox.Unmarshal(events[0], &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.OrderID != "O-1" {
		t.Errorf("OrderID = %q", got.OrderID)
	}
}

func TestAddRequiresKind(t *testing.T) {
	t.Parallel()
	// Nil tx — Add should reject empty kind before touching it.
	err := outbox.Add(context.Background(), nil, "", struct{}{})
	if err == nil {
		t.Fatal("expected error for empty kind")
	}
}

// TestDrainerDispatchesToRiver proves the full pipeline: Add → Pending
// → Dispatcher → river.Insert → Worker runs.
func TestDrainerDispatchesToRiver(t *testing.T) {
	t.Parallel()
	worker := &orderWorker{}
	rv := rivertest.Start(t, rivertest.Options{
		Register: func(w *jobs.Workers) { jobs.Register(w, worker) },
	})
	applyOutboxMigration(t, rv.Pool.Config().ConnConfig.ConnString())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Seed an outbox event.
	tx, err := rv.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := outbox.Add(ctx, tx, "order.created", orderArgs{OrderID: "O-42"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Dispatcher: outbox event → JobArgs via Unmarshal.
	dispatcher := func(_ context.Context, ev outbox.Event) (river.JobArgs, *river.InsertOpts, error) {
		var a orderArgs
		if err := outbox.Unmarshal(ev, &a); err != nil {
			return nil, nil, err
		}
		return a, nil, nil
	}

	drainer := outbox.NewDrainer(rv.Pool, rv.Client, dispatcher,
		slog.New(slog.DiscardHandler), clockwork.NewRealClock(),
		outbox.DrainerOptions{Interval: 100 * time.Millisecond, Batch: 10},
	)

	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	go func() { _ = drainer.Run(runCtx) }()

	// Wait for the worker to see the job.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if worker.seen.Load() >= 1 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("worker.seen = %d after 15s — drainer or river broken", worker.seen.Load())
}

func TestDispatcherNilArgsDropsEvent(t *testing.T) {
	t.Parallel()
	pool := pgtest.Start(t)
	applyOutboxMigration(t, pool.Config().ConnConfig.ConnString())

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := outbox.Add(ctx, tx, "obsolete", json.RawMessage(`{"v":1}`)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	_ = tx.Commit(ctx)

	// Dispatcher that drops everything.
	dispatcher := func(context.Context, outbox.Event) (river.JobArgs, *river.InsertOpts, error) {
		return nil, nil, nil
	}
	drainer := outbox.NewDrainer(pool, nil, dispatcher,
		slog.New(slog.DiscardHandler), clockwork.NewRealClock(),
		outbox.DrainerOptions{Interval: time.Hour, Batch: 10},
	)
	// Call drain once directly.
	if err := drainerDrain(t, drainer, ctx); err != nil {
		t.Fatalf("drain: %v", err)
	}
	events, _ := outbox.Pending(ctx, pool, 10)
	if len(events) != 0 {
		t.Errorf("expected 0 pending after drop-dispatcher, got %d", len(events))
	}
}

// drainerDrain is a test helper that calls the unexported drain method
// via Run for a single interval.
func drainerDrain(t *testing.T, d *outbox.Drainer, ctx context.Context) error {
	t.Helper()
	// Simplest: start Run with a very short interval, run briefly, stop.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- d.Run(runCtx) }()
	time.Sleep(200 * time.Millisecond)
	cancel()
	return <-done
}
