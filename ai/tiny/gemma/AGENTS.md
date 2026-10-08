<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# ai/tiny/gemma

KerasHub Gemma 3 / Gemma 3n LoRA trainer.

## Contract

- Constructor: `NewTrainer(Options) (*Trainer, error)`.
- Required: `Runner`, `Bucket`, digest-pinned `Image`, canonical absolute
  `DatasetRoot`.
- Job: `Modality=text`; `TaskKind=generate`; format `jsonl`.
- Bases: `gemma3:270m`, `gemma3:1b`, `gemma3:4b-text`, `gemma3n:e2b`,
  `gemma3n:e4b`.
- Dataset: local `file:` only; regular file under
  `<DatasetRoot>/<TenantID|_default>`; staged as `/work/input/dataset`.
- JSONL: prompt/response record bytes, aggregate text bytes, examples bounded.
- Runner: read-only input/root; process, tmpfs, output-file caps active;
  network denied unless `AllowNetwork` is explicitly true.
- Preset credentials: runner environment only; dataset cloud credentials
  absent. Uncached KerasHub acquisition requires explicit network opt-in.
- Output: fixed `adapter.lora.h5`; optional fixed `metrics.json`.
- Artifact key:
  `<KeyPrefix>/tenants/<tenant>/<name>/<job>/<sha256>/adapter.lora.h5`.

## Limits

- Timeout: 2h.
- Logs: 1 MiB.
- Artifact: 1 GiB.
- Metrics: 1 MiB.
- Dataset: 10 GiB.
- JSONL record: 1 MiB.
- Aggregate prompt/response UTF-8: 64 MiB.
- Examples: 100,000.

## Proof

- Go: `go test ./ai/tiny/gemma ./ai/tiny/internal/trainerio`.
- Python: `PYTHONPATH=ai/tiny python3 -B -m unittest trainers.gemma.test_trainer`.
- Container: `scripts/ci/tiny-trainer-smoke.sh gemma IMAGE`.
- Release boundary: `ai/tiny/trainers/README.md`.
