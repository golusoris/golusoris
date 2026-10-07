<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# ai/tiny/litert

Keras MobileNet V2 image trainer. Builtin-op LiteRT output.

## Contract

- Constructor: `NewTrainer(Options) (*Trainer, error)`.
- Required: `Runner`, `Bucket`, digest-pinned `Image`, canonical absolute
  `DatasetRoot`.
- Job: `Modality=image`; `TaskKind=classify`; base
  `keras:image/mobilenet-v2`; format `tar|tar.gz|tgz|zip`.
- Dataset: local `file:` only; regular file under
  `<DatasetRoot>/<TenantID|_default>`; staged as `/work/input/dataset`.
- Runner: read-only input/root; network denied; process, tmpfs, output-file
  caps active.
- Output: fixed `model.tflite`; required fixed `metrics.json` with at least two
  unique non-empty labels.
- Artifact key:
  `<KeyPrefix>/tenants/<tenant>/<name>/<job>/<sha256>/model.tflite`.

Deprecated text, MediaPipe, MobileBERT, EfficientNet-Lite constants: compile
compatibility only. Validation rejects every identifier.

## Image runtime

- TensorFlow `2.21.0`; Keras `3.15.1`; Python `3.11.16`.
- `pretrained=false`; digest-pinned weight staging absent.
- Archive: traversal, link, special-file, member, expansion bounds.
- Images: extension plus header match; count, encoded bytes, dimensions,
  decoded pixels, channels bounded; two labels and two images per label.
- Conversion: `TFLITE_BUILTINS` only; `TFL3` check; interpreter allocation
  and finite-output invocation before artifact write.

## Limits

- Timeout: 2h.
- Logs: 1 MiB.
- Artifact: 1 GiB.
- Metrics: 1 MiB.
- Dataset: 10 GiB.

## Proof

- Go: `go test ./ai/tiny/litert ./ai/tiny/internal/trainerio`.
- Python: `PYTHONPATH=ai/tiny python3 -B -m unittest trainers.litert.test_trainer`.
- Container: `scripts/ci/tiny-trainer-smoke.sh litert IMAGE`.
- Release boundary: `ai/tiny/trainers/README.md`.
