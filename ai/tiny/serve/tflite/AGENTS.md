<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# ai/tiny/serve/tflite — AGENTS.md

[tiny.Predictor] that serves LiteRT (`.tflite`) classifiers produced by
[ai/tiny/litert] via Python inference sidecar over HTTP. Thin HTTP
client — sidecar holds TF-Lite interpreter.

## Surface

- `NewPredictor(Options) *Predictor`.
- `(*Predictor).Load(ctx, tiny.Model) error` — validates
 TaskKind=classify + Format=tflite + supported modality + non-empty URI +
 at least two unique non-empty labels, then `POST /load` registers artifact
 with sidecar.
- `(*Predictor).Predict(ctx, input any) (tiny.Prediction, error)` —
 `POST /classify {input}`; maps returned `{scores: {label: prob}}`
 onto sorted `Prediction.Labels` (desc by score, ties by label asc).
- `(*Predictor).Close() error` — no-op (sidecar owns interpreter state).

## Options

| Field              | Default                   | Purpose                                   |
| ------------------ | ------------------------- | ----------------------------------------- |
| `Endpoint`         | `http://127.0.0.1:8501`   | LiteRT sidecar HTTP root.                 |
| `HTTPClient`       | `http.Client{Timeout:30s}`| Override for retries / transport.         |
| `MaxResponseBytes` | 256 KiB                   | Caps `/classify` body read.               |
| `TopK`             | 0 (all)                   | Truncate to K highest-scoring labels.     |

Injected clients are cloned. Positive timeout stays. Zero timeout becomes 30s.

## Sidecar contract

Application-supplied compatible sidecar loads `.tflite` artifact at
`tiny.Model.URI` and exposes:

- `POST /load     {"uri": "...", "labels": [...]}` → 200 on success.
- `POST /classify {"input": <any>}` → `{"scores": {label: prob}}`.
- `GET  /healthz` → 200 when ready.

`Load` accepts only text, image, or audio classifiers and requires at least two
unique non-empty labels before contacting the sidecar.
`Predict` requires the exact loaded label set and finite probabilities in
`[0,1]`; missing or foreign labels fail.

Repository ships no sidecar implementation or image. `golusoris/golusoris#564`
tracks implementation, integration proof, and attested publication.

`input` is forwarded verbatim as JSON; sidecar interprets it per model's modality (text string, image path/bytes, audio samples).

## Runtime status

Pure-Go in-process backend is **deferred**. LiteRT in Go needs CGo bindings to
`libtensorflowlite`, which pulls C toolchain and per-platform build matrix into
every consumer. Client protocol remains usable with an application-owned
compatible endpoint.

## Testing

Tests use `httptest.NewServer` to mock `/load` + `/classify`, covering:
happy path, TopK truncation, score-tie determinism, wrong TaskKind /
Format / modality, empty URI, invalid labels, sidecar 404 on load, server 500,
missing scores, body-limit truncation, Close, default-options path. No Docker
required.

## Don't

- Don't push weights from here — sidecar resolves `Model.URI` itself.
- Don't assume label order from model; always read `Prediction.Labels`
 (already sorted desc by score).
- Don't call `Predict` before `Load` — it errors `Load not called`.
- Don't create inline `http.Client{}` in production. Prefer `extclient.New()` or
  parent `httpx` transport for retry, circuit-breaker, and OTel.
