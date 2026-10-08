<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# riverqueue/river — v0.49.0 snapshot

Pinned: **v0.49.0**
Source: [tagged source](https://github.com/riverqueue/river/tree/v0.49.0)

## Key API surface

### Client

```go
client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
    Queues: map[string]river.QueueConfig{
        river.QueueDefault: {MaxWorkers: 100},
    },
    Workers: river.NewWorkers(),
    Logger:  slog.Default(),
})

// Start / stop
err = client.Start(ctx)
err = client.Stop(ctx)
```

### Defining a worker

```go
type MyArgs struct {
    UserID int64 `json:"user_id"`
}
func (MyArgs) Kind() string { return "my_job" }

type MyWorker struct {
    river.WorkerDefaults[MyArgs]
}
func (w *MyWorker) Work(ctx context.Context, job *river.Job[MyArgs]) error {
    // do work
    return nil
}

// Register
river.AddWorker(workers, &MyWorker{})
```

### Inserting jobs

```go
// Direct insert
_, err = client.Insert(ctx, MyArgs{UserID: 42}, nil)

// In a transaction
_, err = client.InsertTx(ctx, tx, MyArgs{UserID: 42}, &river.InsertOpts{
    Queue:    river.QueueDefault,
    Priority: 1,
    MaxAttempts: 5,
    ScheduledAt: clk.Now().Add(5 * time.Minute),
})
```

### Periodic jobs

```go
periodicJob := river.NewPeriodicJob(
    river.PeriodicInterval(15*time.Minute),
    func() (river.JobArgs, *river.InsertOpts) {
        return MyArgs{}, nil
    },
    &river.PeriodicJobOpts{RunOnStart: true},
)
```

Put `periodicJob` in `river.Config.PeriodicJobs` at construction time. Dynamic
registration uses `client.PeriodicJobs().AddSafely(periodicJob)` and must handle
the returned validation error.

### Error handling

```go
func (w *MyWorker) Work(ctx context.Context, job *river.Job[MyArgs]) error {
    if retryable {
        return fmt.Errorf("transient: %w", err)   // retried up to MaxAttempts
    }
    return river.JobCancel(err)                    // cancels permanently
}
```

### Stop, retry, and fetch controls

```go
cfg := &river.Config{
    RetryPolicy:            myPolicy,      // river.ClientRetryPolicy: NextRetry(*rivertype.JobRow) time.Time
    SoftStopTimeout:        30 * time.Second, // Stop escalates to job-context cancel after this
    LeaderElectionDisabled: false,         // v0.48: work jobs without leading maintenance
    FetchOnlyKnownKinds:    false,         // v0.48: fetch only registered worker kinds
}
err = client.Stop(ctx)          // soft: stop fetching, wait for running jobs
err = client.StopAndCancel(ctx) // hard: cancel running job contexts, then wait
```

### SQLite driver

```go
import "github.com/riverqueue/river/riverdriver/riversqlite"

db.SetMaxOpenConns(1) // upstream advice: avoids SQLITE_BUSY from parallel maintenance
client, err := river.NewClient(riversqlite.New(db), cfg) // *river.Client[*sql.Tx]
```

`JobListParams.Metadata` is unsupported on SQLite. Migration version 8 only
changes SQLite (`river_job` id gains `AUTOINCREMENT`).

### Behaviour changes since v0.47.0

- v0.49: transactional helpers reuse the caller's transaction instead of a
  savepoint; callers must roll back on error (`outbox/drainer.go` already wraps
  `InsertTx` in its own savepoint).
- v0.48: `UniqueOpts.ByPeriod` buckets on the effective scheduled time in UTC;
  `UniqueOpts{ExcludeKind: true}` alone is rejected at insert.

### Transaction helper

```go
// river/riverpgxv5 — pgx driver
import "github.com/riverqueue/river/riverdriver/riverpgxv5"

driver := riverpgxv5.New(pool)
```

## golusoris usage

- `jobs/` — `river.Client` + `river.Workers` provided via fx; periodic job registration.
- `jobs/cron/` — cron expression → `river.PeriodicJob`.
- `testutil/river/` — in-process test harness.

## Links

- [Changelog](https://github.com/riverqueue/river/blob/v0.49.0/CHANGELOG.md)
- [River documentation](https://riverqueue.com/docs)
