# ai/tiny/serve/tflite — AGENTS.md

[tiny.Predictor] that serves LiteRT (`.tflite`) classifiers produced by
[ai/tiny/litert] via a Python inference sidecar over HTTP. Thin HTTP
client — the sidecar holds the TF-Lite interpreter.

## Surface

- `NewPredictor(Options) *Predictor`.
- `(*Predictor).Load(ctx, tiny.Model) error` — validates
  TaskKind=classify + Format=tflite + non-empty URI, then `POST /load`
  registers the artifact with the sidecar.
- `(*Predictor).Predict(ctx, input any) (tiny.Prediction, error)` —
  `POST /classify {input}`; maps the returned `{scores: {label: prob}}`
  onto a sorted `Prediction.Labels` (desc by score, ties by label asc).
- `(*Predictor).Close() error` — no-op (sidecar owns interpreter state).

## Options

| Field              | Default                   | Purpose                                   |
| ------------------ | ------------------------- | ----------------------------------------- |
| `Endpoint`         | `http://127.0.0.1:8501`   | LiteRT sidecar HTTP root.                 |
| `HTTPClient`       | `http.Client{Timeout:30s}`| Override for retries / transport.         |
| `MaxResponseBytes` | 256 KiB                   | Caps `/classify` body read.               |
| `TopK`             | 0 (all)                   | Truncate to K highest-scoring labels.     |

## Sidecar contract

The companion image (`ghcr.io/golusoris/tiny-litert-server`) loads the
`.tflite` artifact at `tiny.Model.URI` and exposes:

- `POST /load     {"uri": "...", "labels": [...]}` → 200 on success.
- `POST /classify {"input": <any>}`               → `{"scores": {label: prob}}`.
- `GET  /healthz`                                  → 200 when ready.

`input` is forwarded verbatim as JSON; the sidecar interprets it per the
model's modality (text string, image path/bytes, audio samples).

## Runtime status

A pure-Go in-process backend is **deferred** — LiteRT in Go needs CGo
bindings to `libtensorflowlite`, which pulls a C toolchain + per-platform
build matrix into every consumer. The sidecar contract is the supported
path; a future in-process `Predictor` can satisfy the same interface
without a consumer change.

## Testing

Tests use `httptest.NewServer` to mock `/load` + `/classify`, covering:
happy path, TopK truncation, score-tie determinism, wrong TaskKind /
Format / empty URI, sidecar 404 on load, server 500, missing scores,
body-limit truncation, Close, default-options path. No Docker required.

## Don't

- Don't push weights from here — the sidecar resolves `Model.URI` itself.
- Don't assume label order from the model; always read `Prediction.Labels`
  (already sorted desc by score).
- Don't call `Predict` before `Load` — it errors `Load not called`.
