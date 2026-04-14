Add a river job worker registered in fx.

## Task

Add a River worker for job kind: `$ARGUMENTS`

## Steps

1. **Define the args struct** in `jobs/<kind>/args.go`:

```go
type Args struct {
    // fields that the enqueuer passes
    UserID string `json:"user_id"`
}

func (Args) Kind() string { return "<kind>" }
```

2. **Implement the worker** in `jobs/<kind>/worker.go`:

```go
type Worker struct {
    river.WorkerDefaults[Args]
    // inject deps via fx
    logger *slog.Logger
}

func (w *Worker) Work(ctx context.Context, job *river.Job[Args]) error {
    // use clock.Now(ctx) — not time.Now()
    // return nil on success; return error to trigger retry
    // return river.JobCancel(err) for permanent failures (no retry)
}
```

3. **Register in fx** — add to the workers registry in `jobs/jobs.go`:

```go
river.AddWorker(workers, &mykind.Worker{...})
```

Or expose an fx.Option in the jobs subpackage:

```go
var WorkerModule = fx.Provide(newWorker) // newWorker returns *Worker with fx deps
```

And add to the worker registry via fx.Invoke.

4. **Write a test** using `testutil/river`:

```go
rc := rivtest.NewTestClient(t, pool, workers)
// enqueue + process synchronously
_, err := rc.Client.Insert(ctx, mykind.Args{UserID: "u1"}, nil)
rc.Work(t)
// assert side effects
```

5. **Lint**: `golangci-lint run ./jobs/<kind>/...` → 0 issues.

## Rules

- Never `panic` inside a worker — return an error.
- Use `river.JobCancel(err)` only when the job can never succeed (bad input).
- Use `clock.Clock` via injection — never `time.Now()`.
