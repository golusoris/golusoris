<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# ai/tiny/trainers/gemma

Gemma KerasHub LoRA container entrypoint.

## Contract

- Dataset: staged local JSONL only; bounded records, examples, aggregate text.
- Job: text generation; allowlisted Gemma 3 and Gemma 3n presets.
- Network: Go runner default deny; explicit `AllowNetwork` for uncached preset
  acquisition only.
- Credentials: environment only; never config, argv, metrics, artifact.
- Output: `adapter.lora.h5`; optional `metrics.json`; atomic bounded writes.
- Contract smoke: I/O only; no preset download; nonroot read-only container.

## Change gate

- Run: `PYTHONPATH=ai/tiny python3 -B -m unittest trainers.gemma.test_trainer`.
- Image: `scripts/ci/tiny-trainer-smoke.sh gemma IMAGE`.
- Vulnerabilities: `bash scripts/ci/tiny-trainer-locks.sh --audit`.
