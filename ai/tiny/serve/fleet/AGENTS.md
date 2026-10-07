<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# ai/tiny/serve/fleet — AGENTS.md

Distributed-inference **recipe** for `ai/tiny`: serve `tiny.Predictor`
across replica set using framework's own `jobs/` (river) queues +
`leader/` election instead of bespoke controller. capability is river queue name; node fan-out is river's fetch model. Apps get
distributed inference by composing existing modules — no hand-rolled
scheduler.

## Topology

- **Controller** (any replica): `Fleet.Submit(ctx, Request)` validates input
 size before insert, resolves `Version=0` once, and queues concrete positive
 version in capability queue `"<prefix>-<capability>"`.
- **Node**: `Module` registers `Worker` on capability queues  node serves and `Client.Queues().Add`s them to running river
 client, so this replica fetches only capability-matched jobs. Worker resolves
 exact model, builds `tiny.Predictor`, runs Predict, and sends exact executed
 model ref to `ResultSink`.

Capability matching is structural: river only delivers job to node
that subscribed to its queue. No node-side filtering loop, no central
dispatcher.

## Surface

| Symbol | Purpose |
| --- | --- |
| `Fleet` / `NewFleet(registry, inserter, prefix)` | Controller with 1 MiB input cap. |
| `NewFleetWithInputLimit(...)` | Controller with explicit encoded-input cap. |
| `(*Fleet).Submit(ctx, Request) (int64, error)` | Validate + enqueue; returns river job ID. |
| `Request{Model, Capability, Input, Priority, Tags}` | One prediction ask. |
| `PredictArgs` | river job payload (wire contract). `Kind() = "golusoris.tiny.fleet.predict"`. |
| `Worker` / `NewWorker(...)` | Node-side river `Worker[PredictArgs]`. |
| `PredictorFactory` | `func(tiny.Model) (tiny.Predictor, error)` — per-job predictor. |
| `SingletonFactory(p)` | Share one predictor; serialize each Load+Predict lease; no per-job Close. |
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

- controller streams JSON sizing to a discard writer; null counts as 4 bytes;
 oversized/non-JSON input → synchronous rejection before insert.
- worker capability mismatch / oversized legacy payload → `river.JobCancel`.
- **Model resolve / Load / Predict / Sink** errors → plain error → river
 retries per its backoff.
- controller unknown/unresolved model fails synchronously. Queued latest refs
 never drift after newer model publication.

## Don't

- Don't put `.` (or uppercase) in capability — river queue names are
 `[a-z0-9_-]`. `Submit` / `NewWorker` normalize case + reject rest.
- Don't copy vmafx's SQLite controller. queue + leader modules
 already provide durable scheduling, retries, and graceful drain.
- Don't assume `SingletonFactory`'s predictor is Closed per job — it is
 process-wide and runs one Load+Predict lease at a time. Use plain
 `PredictorFactory` for parallel per-job predictors.
- Don't run training here — this is inference half. Trainers live in
 `ai/tiny/gemma` + `ai/tiny/litert`.
