<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# ai/tiny/serve — AGENTS.md

Inference adapters for `tiny.Predictor`. Each subpackage wraps specific runtime so loaded `tiny.Model` artifacts can be served via
unified `tiny.Predictor` interface.

## Layout

```
serve/
  internal/httpoptions/ # shared bounded HTTP option normalization
  ollama/   # Ollama HTTP API → Gemma / Gemma 3n (text, generate)
  tflite/   # HTTP client protocol for app-supplied LiteRT classifier sidecar
  fleet/    # distributed-inference recipe: Predictor over jobs/ + leader/
```

`fleet/` subpackage is not adapter — it is recipe that wires
any `tiny.Predictor` behind river worker + capability-matched queue so
apps serve inference across replica set without bespoke controller.

## Contract

All adapters implement:

```go
type Predictor interface {
    Load(ctx context.Context, m tiny.Model) error
    Predict(ctx context.Context, input any) (tiny.Prediction, error)
    Close() error
}
```

Each adapter validates incoming `tiny.Model` against modality
and task kind it supports, then serves predictions against its
runtime. `Close` frees runtime resources; for stateless HTTP adapters
it is no-op.

HTTP clients: clone caller client. Preserve positive timeout. Replace zero
timeout with adapter bound: Ollama 60s; LiteRT 30s.

## Why separate from the trainers

Training and inference have different runtime shapes: trainers are
one-shot batch containers, serving is long-running process (or HTTP
client). Keeping them in sibling packages lets apps opt into predictor they need without pulling in training deps.
