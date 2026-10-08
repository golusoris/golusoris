<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# ai/tiny

Framework for training and serving small task-specific models.
Shipped trainers: Gemma text generation; LiteRT image classification.
`ModalityAudio` remains model metadata; no audio trainer implementation.

Go orchestrates; Python does heavy lifting. Each `Trainer`
implementation spawns container (docker / k8s Job) with pinned
Python toolchain, captures artifact into `Registry`, and serves
it via `Predictor`.

## Surface

- `tiny.Job{ID, Name, TenantID, Dataset, BaseModel, Hyperparams, Tags}` —
 training run spec.
- `tiny.Model{ID, JobID, Name, Version, URI, Format, Modality, TaskKind, Labels, Metrics}` —
 trained artifact.
- `tiny.Dataset{URI, Format, Modality, TaskKind, …}` — materialized corpus ref.
- `tiny.Trainer` — `Train(ctx, Job) (Model, error)`. Implementations
 live in sibling packages.
- `tiny.Predictor` — `Load` + `Predict` + `Close`.
- `tiny.Registry` — `SaveJob` / `SaveModel` / `GetModel` / `Latest` / `List`.
- `tiny.MemoryRegistry` — in-process, tests + local dev.
- `tiny.PGRegistry` — durable, per-tenant Postgres backend
 (`NewPGRegistry(pool)` / `NewPGRegistryWithClock`). Apply
 `ai/tiny/migrations` (exposed via `tiny.MigrationsFS`) before use.
- `tiny.Module` — fx module providing `tiny.Registry` (`PGRegistry`)
 from injected `*pgxpool.Pool` + `clock.Clock`.
- `tiny.ValidateJob(Job) error` — pre-flight schema check.
- `tiny.ValidateClassifierLabels([]string) error` — require at least two unique,
  non-empty external labels.
- `tiny.Runner` / `tiny.RunSpec` — container execution seam. Images require
 exact `@sha256:` digests unless caller sets `AllowUnpinnedImage` for local
 trust. Zero timeout becomes finite `tiny.DefaultRunTimeout` (2h).
 `NetworkDisabled` overrides runner network configuration;
 `MaxOutputFileBytes` becomes container `RLIMIT_FSIZE`.

## Layout

- `ai/tiny/` — core interfaces + MemoryRegistry + PGRegistry + Module +
 `Runner` abstraction.
- `ai/tiny/migrations/` — golang-migrate pair for PGRegistry schema
 (`golusoris_tiny_jobs` + `golusoris_tiny_models`), embedded as
 `tiny.MigrationsFS`.
- `ai/tiny/gemma/` — Gemma LoRA fine-tuning (generative).
- `ai/tiny/litert/` — Keras MobileNet V2 image classification; builtin-op
 LiteRT conversion.
- `ai/tiny/trainers/` — separate Gemma and LiteRT image contexts; shared
 bounded URI/archive contract; Python contract regressions.
- `ai/tiny/serve/ollama/` — Predictor for Gemma via Ollama HTTP API.
- `ai/tiny/serve/tflite/` — client protocol for application-supplied LiteRT
  classifier sidecar. No runtime image ships; tracked by issue #564.
- `ai/tiny/serve/fleet/` — distributed-inference recipe: serve  Predictor across replicas over `jobs/` (river) + `leader/`,
 capability-matched via queue names.

## Design notes

- **Version allocation:** Registries MUST assign `Model.Version`
 monotonically per `(TenantID, Name)` so apps can pin working
 version while training replacements. `MemoryRegistry` does this
 in-process; `PGRegistry` reads `max(version)+1` and lets unique
 index `(tenant_id, name, version)` reject racing writers, which retry
 with fresh max (bounded loop). `tenant_id` is stored as `''` (never
 NULL) for single-tenant case so index + `Latest` stay simple.
- **Modality + TaskKind are required** so Trainer knows which
 Python toolchain / base model to load and so Registry can filter
 models at retrieval time without opening artifact bytes.
- **Dataset URI:** local `file:` only. Trainer `Options.DatasetRoot`: required
 canonical absolute directory. Dataset path: regular file beneath
 `<DatasetRoot>/<TenantID|_default>`; copied to `/work/input/dataset`.
 Remote acquisition belongs before `Trainer.Train`.
- **No Go-native training.** Apps needing on-device training integrate
 with platform SDKs directly (Kotlin/Swift) — this package
 targets server-side training + inference.
- **Zero-Model semantics:** on `Trainer.Train` failure returned
 Model must be zero-valued; callers MUST NOT persist it.
- **Container secret boundary:** Docker argv carries only a private env-file
 path; values never enter Docker CLI argv or environment. Environment names and
 values obey Docker's bounded env-file grammar. Trainer output stays
 regular-file confined and byte-capped.
- **Image release boundary:** no default image until issue #563 publishes and
 records immutable digests. Production `Options.Image`: exact `@sha256:` only.

## SaveJob migration

Public break: `SaveJob` now mutates the supplied job so generated identity and
timestamp reach the caller.

```go
// Before: generated ID lost in registry's value copy.
err := registry.SaveJob(ctx, job)

// After: job.ID and job.CreatedAt populated before return.
err := registry.SaveJob(ctx, &job)
```

`Train` requires non-empty safe single-segment `Job.ID` and `Job.Name`;
non-empty `TenantID` follows the same rule. Call `SaveJob` first when an ID is
needed. `Dataset.SchemaHint` and `Hyperparams` are canonical JSON values only:
`nil`, `bool`, `float64`, `string`, `[]any`, `map[string]any`. Integers,
`[]byte`, `time.Time`, typed slices, functions, and other Go-only values fail
at `SaveJob`; encoded schema hints, hyperparameters, and tags share a 1 MiB
cap. This keeps MemoryRegistry and PGRegistry snapshots identical and bounds
snapshot cloning before allocation.

## Dataset staging migration

Public break: Gemma and LiteRT constructors now require `Options.DatasetRoot`.
Stage every dataset beneath `<DatasetRoot>/<TenantID>` or
`<DatasetRoot>/_default`; pass its absolute `file:` URI. HTTPS and S3 dataset
URIs now fail before runner invocation.

LiteRT training supports only `litert.BaseImageMobileNetV2`
(`keras:image/mobilenet-v2`). Deprecated text and MediaPipe constants remain
declared for source compatibility; submitted jobs fail validation.
