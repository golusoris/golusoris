# ai/tiny/serve/ollama — AGENTS.md

[tiny.Predictor] that routes generate calls to an Ollama HTTP
endpoint. Intended to serve Gemma / Gemma 3n base checkpoints + LoRA
adapters produced by [ai/tiny/gemma].

## Surface

- `NewPredictor(Options) *Predictor`.
- `(*Predictor).Load(ctx, tiny.Model) error` — validates
  Modality=text + TaskKind=generate, then calls `/api/show` to verify
  the ollama tag is registered.
- `(*Predictor).Predict(ctx, input any) (tiny.Prediction, error)` —
  posts `{model, prompt, stream:false}` to `/api/generate`. Input must
  be a string.
- `(*Predictor).Close() error` — no-op (stateless HTTP).

## Options

| Field              | Default                    | Purpose                                      |
| ------------------ | -------------------------- | -------------------------------------------- |
| `Endpoint`         | `http://127.0.0.1:11434`   | Ollama HTTP API root.                        |
| `HTTPClient`       | `http.Client{Timeout:60s}` | Override for retries / transport.            |
| `MaxResponseBytes` | 256 KiB                    | Caps `/api/generate` body read.              |
| `TagOverride`      | ""                         | Use when registry name ≠ ollama tag.         |

## Assumptions

- Ollama instance has the model tag already registered. This package
  is a thin client — it does not `ollama create` or push weights.
- Ollama's response JSON has a `response` field (non-streaming mode).
- Model versioning is expressed via distinct ollama tags
  (e.g. `intent-v3`) rather than inline version params.

## Testing

Tests use `httptest.NewServer` to mock `/api/show` + `/api/generate`,
covering: happy path, tag override, 404 missing model, wrong
TaskKind / Modality, empty name, not-loaded, non-string input, server
error, body-limit truncation, Close.
