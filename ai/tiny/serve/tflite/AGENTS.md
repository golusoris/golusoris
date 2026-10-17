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

Compatible sidecar loads `.tflite` artifact at `tiny.Model.URI` and exposes:

- `POST /load     {"uri": "...", "sha256": "...", "labels": [...]}` → 200 on success.
- `POST /classify {"image": "<base64>"}` → `{"scores": {label: prob}}`.
- `GET  /healthz` → 200 when ready.

`Load` requires TaskKind=classify, Format=tflite, Modality=image, expected SHA-256
in `Model.Metadata["sha256"]`, non-empty URI, and at least two unique non-empty labels.
`Predict` accepts encoded image bytes (`[]byte`) or base64 string, sends typed
`{"image": "<base64>"}`, and validates finite probabilities in `[0,1]` matching loaded labels.

Sidecar implementation ships in `ai/tiny/serve/tflite/sidecar/`.

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

## Sidecar publication and verification

- Image: `ghcr.io/golusoris/tiny-litert-sidecar`.
- Tag namespace: `tiny-litert-sidecar-vX.Y.Z`.
- Gate: `vars.TINY_LITERT_SIDECAR_PUBLISH_ENABLED == true`, environment `tiny-litert-sidecar-publish`.
- Verify: `bash scripts/ci/tiny-trainer-verify-image.sh ghcr.io/golusoris/tiny-litert-sidecar@sha256:... golusoris/golusoris <sha> refs/tags/tiny-litert-sidecar-vX.Y.Z tiny-litert-sidecar-image.yml tiny-litert-sidecar-`.
