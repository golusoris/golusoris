# riverqueue/river — v0.34.0 snapshot

Pinned: **v0.34.0**
Source: https://pkg.go.dev/github.com/riverqueue/river@v0.34.0

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
    ScheduledAt: time.Now().Add(5 * time.Minute),
})
```

### Periodic jobs

```go
&river.PeriodicJob{
    ConstructorFunc: func() (river.JobArgs, *river.InsertOpts) {
        return MyArgs{}, nil
    },
    RunImmediately: true,
    Schedule:       river.ScheduleFunc(func(t time.Time) time.Time {
        return t.Add(15 * time.Minute)
    }),
}
```

### Error handling

```go
func (w *MyWorker) Work(ctx context.Context, job *river.Job[MyArgs]) error {
    if retryable {
        return fmt.Errorf("transient: %w", err)   // retried up to MaxAttempts
    }
    return river.JobCancel(err)                    // cancels permanently
}
```

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

- Changelog: https://github.com/riverqueue/river/blob/master/CHANGELOG.md
- Docs: https://riverqueue.com/docs
