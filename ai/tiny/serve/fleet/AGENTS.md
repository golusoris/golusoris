# ai/tiny/serve/fleet — AGENTS.md

Distributed-inference **recipe** for `ai/tiny`: serve a `tiny.Predictor`
across a replica set using the framework's own `jobs/` (river) queues +
`leader/` election instead of a bespoke controller. A capability is a
river queue name; node fan-out is river's fetch model. Apps get
distributed inference by composing existing modules — no hand-rolled
scheduler.

## Topology

- **Controller** (any replica): `Fleet.Submit(ctx, Request)` resolves the
  model against the `tiny.Registry` (fail-fast on a bad model) and
  inserts a `PredictArgs` river job into the capability-matched queue
  `"<prefix>-<capability>"`.
- **Node**: `Module` registers a `Worker` on the capability queues the
  node serves and `Client.Queues().Add`s them to the running river
  client, so this replica fetches only capability-matched jobs. The
  worker resolves the model, builds a `tiny.Predictor` via a
  `PredictorFactory`, runs Predict, and hands the result to a
  `ResultSink`.

Capability matching is structural: river only delivers a job to a node
that subscribed to its queue. No node-side filtering loop, no central
dispatcher.

## Surface

| Symbol | Purpose |
| --- | --- |
| `Fleet` / `NewFleet(registry, inserter, prefix)` | Controller handle. |
| `(*Fleet).Submit(ctx, Request) (int64, error)` | Validate + enqueue; returns river job ID. |
| `Request{Model, Capability, Input, Priority, Tags}` | One prediction ask. |
| `PredictArgs` | river job payload (wire contract). `Kind() = "golusoris.tiny.fleet.predict"`. |
| `Worker` / `NewWorker(...)` | Node-side river `Worker[PredictArgs]`. |
| `PredictorFactory` | `func(tiny.Model) (tiny.Predictor, error)` — per-job predictor. |
| `SingletonFactory(p)` | Share one concurrency-safe predictor; no per-job Close. |
| `ResultSink` / `ResultSinkFunc` | Persist/forward a finished `tiny.Prediction`. |
| `Capability` | Opaque node trait → queue name. Lowercase `[a-z0-9_-]`. |
| `Module` | fx wiring: provides `*Fleet`, registers the node `Worker` + queues. |

## Wiring

```go
fx.New(
    golusoris.Core,
    golusoris.DB,
    jobs.Module, // *jobs.Client + *jobs.Workers (river)
    // tiny.Registry — your durable backend, or MemoryRegistry for dev.
    fx.Supply(fx.Annotate(myRegistry, fx.As(new(tiny.Registry)))),
    // PredictorFactory — the common case shares one ollama client.
    fx.Provide(func(p *ollama.Predictor) fleet.PredictorFactory {
        return fleet.SingletonFactory(p)
    }),
    // ResultSink — where predictions land.
    fx.Provide(func(db *pgxpool.Pool) fleet.ResultSink {
        return fleet.ResultSinkFunc(func(ctx context.Context, ref tiny.Ref, pr tiny.Prediction) error {
            return storePrediction(ctx, db, ref, pr)
        })
    }),
    fleet.Module,
)
```

Config keys (env `APP_TINY_FLEET_*`):

| Key | Default | Purpose |
| --- | --- | --- |
| `tiny.fleet.enabled` | `true` | Master switch. |
| `tiny.fleet.queue_prefix` | `tiny` | Queue-name prefix. |
| `tiny.fleet.capabilities` | `["cpu"]` | This node's served capabilities. |
| `tiny.fleet.max_workers` | `4` | Per-capability concurrent workers. |
| `tiny.fleet.predict_timeout` | `60s` | Caps one Load+Predict. |
| `tiny.fleet.max_input_bytes` | 1 MiB | Caps the re-encoded job input. |

## Failure semantics

- **Capability mismatch** / **oversized input** → `river.JobCancel`
  (permanent; retrying cannot help; it is a routing/producer bug).
- **Model resolve / Load / Predict / Sink** errors → plain error → river
  retries per its backoff.
- A controller `Submit` against an unknown model fails synchronously
  (no queue round-trip).

## Don't

- Don't put `.` (or uppercase) in a capability — river queue names are
  `[a-z0-9_-]`. `Submit` / `NewWorker` normalize case + reject the rest.
- Don't copy vmafx's SQLite controller. The queue + leader modules
  already provide durable scheduling, retries, and graceful drain.
- Don't assume `SingletonFactory`'s predictor is Closed per job — it is
  not (it is process-wide). Use a plain `PredictorFactory` when each job
  needs a fresh, Closed predictor.
- Don't run training here — this is the inference half. Trainers live in
  `ai/tiny/gemma` + `ai/tiny/litert`.
