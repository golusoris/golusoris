<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# ai/tiny/serve/onnx — AGENTS.md

`tiny.Predictor` adapter executing ONNX graphs via `vmafx-ort-runner` subprocess protocol.
No cgo; pure Go runner boundary.

## Surface

- `NewPredictor(Options) *Predictor`.
- `(*Predictor).Load(ctx, tiny.Model) error` — validates `TaskKind` (`regress`, `classify`, `embed`), resolves model path from URI, verifies regular file, resolves runner executable via `exec.LookPath`.
- `(*Predictor).Predict(ctx, input any) (tiny.Prediction, error)` — passes float slice to runner, decodes JSON output tensor, maps to prediction.
- `(*Predictor).Close() error` — no-op.
- Sentinels: `ErrRunnerUnavailable` (exit 3 or missing runner), `ErrModelExecution` (exit 1), `ErrRunnerUsage` (exit 2).

## Options

| Field | Default | Purpose |
| :--- | :--- | :--- |
| `RunnerPath` | `vmafx-ort-runner` | Runner executable path or name. |
| `InputName` | `""` | Graph input name override (`--input-name`). |
| `Timeout` | `10s` | Subprocess timeout when context deadline absent. |
| `Labels` | `nil` | Expected classifier labels override. |

## Protocol

- Invocation: `<runner> --model <path.onnx> --inputs '<JSON array>' [--input-name <name>]`
- Stdout: single JSON number array plus newline.
- Stderr: diagnostics text.
- Exit codes: 0 success, 1 model execution failure, 2 usage/protocol error, 3 built without ORT.

## Bounds

- Input length: <= 4096 floats (`MaxInputLength`).
- Output length: <= 65536 floats (`MaxOutputLength`).
- Stdout cap: 1 MiB (`MaxStdoutBytes`).
- Stderr cap: 4 KiB (`MaxStderrBytes`).
- Post-cancel grace: 2s `cmd.WaitDelay`.
