# ai/tiny/litert — AGENTS.md

MediaPipe Model Maker trainer producing LiteRT (`.tflite`) artifacts.
Implements `tiny.Trainer`.

## Surface

- `NewTrainer(Options) (*Trainer, error)` — validates `Runner` + `Bucket`.
- `(*Trainer).Name() string` → `"litert"`.
- `(*Trainer).Train(ctx, tiny.Job) (tiny.Model, error)`.

## Supported jobs

| Modality | TaskKind | MediaPipe task / backbone |
|---|---|---|
| `text` | `classify` | `text_classifier` (MobileBERT / AverageWordEmbedding) |
| `image` | `classify` | `image_classifier` (EfficientNet-Lite) |
| `audio` | `classify` | `audio_classifier` (YAMNet) |

Other combinations are rejected at `Train` entry.

## Flow

1. `tiny.ValidateJob` + enforce `TaskKind=classify` + supported modality.
2. Stage tmpdir with `input/config.json` (job id, modality/task, dataset URI, hyperparams).
3. Invoke `Runner.Run` with `/work/input` (ro) + `/work/output` (rw), env seeded with `TINY_JOB_NAME` / `TINY_MODALITY` / `TINY_TASK` plus `ExtraEnv`.
4. Drain trainer stdout/stderr into `slog` via `bytes.Buffer` + `bufio.Scanner`.
5. Read `output/model.tflite` + `output/metrics.json` (sidecar with `{metrics: {...}, labels: [...]}` shape).
6. Upload bundle to `<KeyPrefix>/<job.Name>/<job.ID>/model.tflite`.
7. Return `tiny.Model` with `Format=TFLite`, `Labels`, `Metrics`, tag metadata.

## Defaults

- `Image`: `ghcr.io/golusoris/tiny-litert-trainer:v1`.
- `KeyPrefix`: `models/litert`.
- `Logger`: `slog.Default()`.

## Metrics sidecar

```json
{
  "metrics": {"accuracy": 0.93, "loss": 0.18, "epochs": 3},
  "labels":  ["spam", "ham"]
}
```

Parse failures warn but never fail the run — the `.tflite` artifact is the authoritative output.

## Testing

`StubRunner` writes `model.tflite` + `metrics.json` into `spec.OutputDir`; the test round-trips through `storage.LocalBucket` and asserts the bucketed bytes match. No Docker required for unit tests.
