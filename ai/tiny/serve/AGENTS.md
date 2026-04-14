# ai/tiny/serve — AGENTS.md

Inference adapters for `tiny.Predictor`. Each subpackage wraps a
specific runtime so loaded `tiny.Model` artifacts can be served via
the unified `tiny.Predictor` interface.

## Layout

```
serve/
  ollama/   # Ollama HTTP API → Gemma / Gemma 3n (text, generate)
  tflite/   # (pending) Python sidecar for LiteRT models (classify)
```

## Contract

All adapters implement:

```go
type Predictor interface {
    Load(ctx context.Context, m tiny.Model) error
    Predict(ctx context.Context, input any) (tiny.Prediction, error)
    Close() error
}
```

Each adapter validates the incoming `tiny.Model` against the modality
and task kind it supports, then serves predictions against its
runtime. `Close` frees runtime resources; for stateless HTTP adapters
it is a no-op.

## Why separate from the trainers

Training and inference have different runtime shapes: trainers are
one-shot batch containers, serving is a long-running process (or HTTP
client). Keeping them in sibling packages lets apps opt into just the
predictor they need without pulling in training deps.
