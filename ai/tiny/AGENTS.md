# ai/tiny

Framework for training and serving small task-specific models —
text/image/audio classifiers, embedding extractors, and tiny
generative LMs (Gemma 3 270M/1B, Gemma 3n E2B) fine-tuned for one job.

Go orchestrates; Python does the heavy lifting. Each `Trainer`
implementation spawns a container (docker / k8s Job) with a pinned
Python toolchain, captures the artifact into a `Registry`, and serves
it via a `Predictor`.

## Surface

- `tiny.Job{ID, Name, TenantID, Dataset, BaseModel, Hyperparams, Tags}` —
  a training run spec.
- `tiny.Model{ID, JobID, Name, Version, URI, Format, Modality, TaskKind, Labels, Metrics}` —
  a trained artifact.
- `tiny.Dataset{URI, Format, Modality, TaskKind, …}` — materialized corpus ref.
- `tiny.Trainer` — `Train(ctx, Job) (Model, error)`. Implementations
  live in sibling packages.
- `tiny.Predictor` — `Load` + `Predict` + `Close`.
- `tiny.Registry` — `SaveJob` / `SaveModel` / `GetModel` / `Latest` / `List`.
- `tiny.MemoryRegistry` — in-process, tests + local dev.
- `tiny.PGRegistry` — durable, per-tenant Postgres backend
  (`NewPGRegistry(pool)` / `NewPGRegistryWithClock`). Apply
  `ai/tiny/migrations` (exposed via `tiny.MigrationsFS`) before use.
- `tiny.Module` — fx module providing `tiny.Registry` (the `PGRegistry`)
  from an injected `*pgxpool.Pool` + `clock.Clock`.
- `tiny.ValidateJob(Job) error` — pre-flight schema check.

## Layout

- `ai/tiny/` — core interfaces + MemoryRegistry + PGRegistry + Module +
  `Runner` abstraction.
- `ai/tiny/migrations/` — golang-migrate pair for the PGRegistry schema
  (`golusoris_tiny_jobs` + `golusoris_tiny_models`), embedded as
  `tiny.MigrationsFS`.
- `ai/tiny/gemma/` — Gemma LoRA fine-tuning (generative).
- `ai/tiny/litert/` — MediaPipe Model Maker (text/image/audio classify).
- `ai/tiny/serve/ollama/` — Predictor for Gemma via Ollama HTTP API.
- `ai/tiny/serve/tflite/` — Predictor for LiteRT classifiers via a Python
  inference sidecar (in-process Go runtime deferred — see its AGENTS.md).
- `ai/tiny/serve/fleet/` — distributed-inference recipe: serve a
  Predictor across replicas over `jobs/` (river) + `leader/`,
  capability-matched via queue names.

## Design notes

- **Version allocation:** Registries MUST assign `Model.Version`
  monotonically per `(TenantID, Name)` so apps can pin a working
  version while training replacements. `MemoryRegistry` does this
  in-process; `PGRegistry` reads `max(version)+1` and lets the unique
  index `(tenant_id, name, version)` reject racing writers, which retry
  with a fresh max (bounded loop). `tenant_id` is stored as `''` (never
  NULL) for the single-tenant case so the index + `Latest` stay simple.
- **Modality + TaskKind are required** so the Trainer knows which
  Python toolchain / base model to load and so the Registry can filter
  models at retrieval time without opening artifact bytes.
- **URI is opaque to Go** — the Trainer container resolves it
  (`s3://…`, `gs://…`, `file:///…`, etc.) using whatever creds are
  mounted into the job.
- **No Go-native training.** Apps needing on-device training integrate
  with the platform SDKs directly (Kotlin/Swift) — this package
  targets server-side training + inference.
- **Zero-Model semantics:** on `Trainer.Train` failure the returned
  Model must be zero-valued; callers MUST NOT persist it.
