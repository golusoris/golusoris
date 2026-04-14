# ai/tiny/gemma — AGENTS.md

LoRA fine-tune trainer for Gemma 3 / Gemma 3n. Implements `tiny.Trainer`.

## Surface

- `NewTrainer(Options) (*Trainer, error)` — validates `Runner` + `Bucket`.
- `(*Trainer).Name() string` → `"gemma"`.
- `(*Trainer).Train(ctx, tiny.Job) (tiny.Model, error)`.

## Flow

1. `tiny.ValidateJob` + require `Modality=text`, `TaskKind=generate`, `BaseModel` prefix `gemma3:` or `gemma3n:`.
2. Stage tmpdir with `input/config.json` (job id, base model, dataset URI, hyperparams, tags).
3. Invoke `Runner.Run` with `/work/input` (ro) + `/work/output` (rw), env seeded with `TINY_JOB_NAME` / `TINY_BASE_MODEL` plus `ExtraEnv` (non-overridable reserved keys).
4. Stream trainer stdout/stderr into a `bytes.Buffer`, drain into `slog` after `Run` returns (success or failure).
5. Read `output/lora.keras` + optional `output/metrics.json`, upload bundle to `<KeyPrefix>/<job.Name>/<job.ID>/lora.keras`.
6. Return `tiny.Model` with `Format=KerasLoRA`, metrics map, tag metadata; `Version` left zero for `Registry.SaveModel`.

## Supported base models

`gemma3:270m`, `gemma3:1b`, `gemma3:4b`, `gemma3n:e2b`, `gemma3n:e4b`. Container resolves checkpoints at runtime; expects `HF_TOKEN` in env.

## Artifact

- `lora.keras` (KerasNLP LoRA weight archive) in the bucket.
- `metrics.json` parsed into `Model.Metrics` (best-effort — parse failure only warns).

## Defaults

- `Image`: `ghcr.io/golusoris/tiny-gemma-trainer:v1`.
- `KeyPrefix`: `models/gemma`.
- `Logger`: `slog.Default()`.

## Testing

`StubRunner` simulates the container by writing `lora.keras` + `metrics.json` into `spec.OutputDir`. Round-trip through `storage.LocalBucket` exercises the upload path without Docker.
