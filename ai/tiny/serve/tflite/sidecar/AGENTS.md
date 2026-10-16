<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# ai/tiny/serve/tflite/sidecar

LiteRT HTTP inference sidecar runtime, container, and test suite.

## Contract

- Endpoints:
  - `POST /load`: JSON `{"uri": "...", "sha256": "...", "labels": [...]}`; resolves `https` (bounded bytes and time, strict same-host redirect) or `file` (under mounted model dir); verifies SHA-256; validates `TFL3` header; allocates interpreter; requires input shape `[1, 160, 160, 3]` and output shape `[1, len(labels)]`; atomic swap on success; failed reload keeps active model.
  - `POST /classify`: JSON `{"image": "<base64>"}`; decodes PNG/JPEG; dimensions <= 8192, pixels <= 4096*4096; resizes to 160x160 RGB; input raw 0..255 by design (trainer model carries Rescaling layer `ai/tiny/trainers/litert/trainer.py:330`); executes interpreter; returns `{"scores": {label: prob}}`.
  - `GET /healthz`: readiness and backend version; returns 200 `{"status": "ready", "backend": "ai-edge-litert <version>"}` or 503 `{"status": "not_ready", "backend": "..."}`; leaks zero metadata, credentials, model URI, or inputs.
- Concurrency: bounded semaphore cap; per-connection socket timeout; interpreter lock per active model; concurrent classify and reload race-free.
- Deployment: binds 0.0.0.0 inside container, zero authn -> requires network policy; digest-pinned base `python:3.11.16-slim-bookworm`; hash-locked dependencies (`requirements.lock`); non-root UID `65532:65532`; `--read-only` rootfs + tmpfs `/tmp`.
- Prerequisites: python environment with `pip install -r requirements.lock`; `uv` for model generation; missing deps fail startup with clear ImportError.

## Change gate

- Lint: `ruff check ai/tiny/serve/tflite/sidecar && ruff format --check ai/tiny/serve/tflite/sidecar`.
- Unit tests: `pytest ai/tiny/serve/tflite/sidecar`.
- Container build: `docker build -t golusoris-litert-sidecar:test ai/tiny/serve/tflite/sidecar/`.
- Integration smoke: `bash scripts/ci/tiny-litert-sidecar-smoke.sh`.

