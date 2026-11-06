<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Deploy the LiteRT sidecar

The sidecar serves image classifiers exported by the [LiteRT trainer](../../../trainers/litert/trainer.py).
Place the trainer's `model.tflite` and `metrics.json` in a local `models` directory, readable by UID 65532.
Run the published v0.1.1 image by its immutable digest:

```bash
docker run -d --rm --name tiny-litert-sidecar \
  --read-only --user 65532:65532 \
  --cap-drop=ALL --security-opt=no-new-privileges \
  --pids-limit=256 --memory=2g --cpus=2 \
  --tmpfs /tmp:rw,noexec,nosuid,size=1g,mode=1777 \
  -p 127.0.0.1:8501:8501 \
  --mount "type=bind,src=$(pwd)/models,dst=/models,readonly" \
  ghcr.io/golusoris/tiny-litert-sidecar@sha256:4ad6d318b0950a574cf5919532becc1da4379682b45e276335acbb4983fae835
```

The read-only filesystem, UID, capability restrictions, PID limit and bounded 1 GiB `/tmp` match the [integration smoke](../../../../../scripts/ci/tiny-litert-sidecar-smoke.sh).
The example additionally caps memory at 2 GiB and CPU at two cores; size these limits for your workload.
The server binds port 8501 on all container interfaces without authentication.
Keep the host binding on loopback or apply network policy so only trusted applications can load models and classify images.
The [server defaults](server.py) allow eight concurrent operations and use a ten-second connection timeout.
The [runtime](runtime.py) serializes interpreter calls per loaded model.

## Load an artifact

Use the Go [Predictor](../AGENTS.md) with `Modality=image`, `TaskKind=classify`, `Format=tflite`, the artifact URI, trainer `metrics.json` labels in their original order, and the artifact's SHA-256 in `Model.Metadata["sha256"]`.
`Predictor.Load` sends `POST /load` with this JSON contract:

```json
{"uri":"file:///models/model.tflite","sha256":"<64 hexadecimal characters>","labels":["<first trainer label>","<second trainer label>"]}
```

For a direct HTTP load, generate the request from the trainer output:

```bash
python3 - <<'PY' > load.json
import hashlib
import json
from pathlib import Path

root = Path("models")
print(json.dumps({
    "uri": "file:///models/model.tflite",
    "sha256": hashlib.sha256((root / "model.tflite").read_bytes()).hexdigest(),
    "labels": json.loads((root / "metrics.json").read_text())["labels"],
}))
PY
curl --fail-with-body -H 'Content-Type: application/json' \
  --data-binary @load.json http://127.0.0.1:8501/load
curl --fail-with-body http://127.0.0.1:8501/healthz
```

The [runtime artifact contract](runtime.py) accepts `file` locators confined to `/models`, or HTTPS locators with same-host HTTPS redirects only.
`LITERT_MODEL_DIR` can override the mounted model directory.
Artifacts are limited to 64 MiB; HTTPS acquisition has a 30-second default timeout.
The runtime verifies SHA-256 and the `TFL3` FlatBuffer header before allocating the interpreter.
Labels must contain at least two unique, nonempty strings and preserve the trainer's sorted folder order.
The model must expose one input shaped `[1, 160, 160, 3]` and one output shaped `[1, label count]` or `[label count]`.
The [server](server.py) caps `POST /load` JSON at 1 MiB.
A successful load atomically replaces the active model; a failed reload keeps the previous model.

`GET /healthz` returns HTTP 503 before a model is loaded and HTTP 200 afterward, with status and the `ai-edge-litert` backend version.
It reports readiness without exposing model metadata or inputs.

## Classify an image

`POST /classify` accepts JSON containing `image`, a strict base64 encoding of PNG or JPEG bytes:

```json
{"image":"<base64 PNG or JPEG>"}
```

The response contains scores keyed by the loaded labels:

```json
{"scores":{"<first trainer label>":0.9,"<second trainer label>":0.1}}
```

The [server](server.py) limits the request body to 20 MiB.
The [runtime](runtime.py) limits the base64 field to 16 MiB of characters, decoded bytes to 64 MiB, each dimension to 8192, and total pixels to 16,777,216.
The base64 limit is the tighter effective encoded-image bound.
Images are converted to RGB and resized to 160 by 160.
Pixel values remain in the 0–255 range because the trainer embeds its own rescaling layer.
Go `Predictor.Predict` accepts encoded image bytes or a base64 string and returns labels sorted by descending score.

## Upgrade

Resolve the new release's immutable image digest and record its source commit and tag from the publication workflow.
Follow the [publication identity contract](AGENTS.md#publication-and-verification) and run the verifier from the repository root before deployment:

```bash
bash scripts/ci/tiny-trainer-verify-image.sh \
  ghcr.io/golusoris/tiny-litert-sidecar@sha256:4ad6d318b0950a574cf5919532becc1da4379682b45e276335acbb4983fae835 \
  golusoris/golusoris <source-commit-sha> \
  refs/tags/tiny-litert-sidecar-v0.1.1 \
  tiny-litert-sidecar-image.yml tiny-litert-sidecar-
```

For each upgrade, substitute the newly resolved digest, source commit and source tag together.
The [verifier](../../../../../scripts/ci/tiny-trainer-verify-image.sh) checks the Cosign signature, SLSA provenance and SPDX SBOM against the publication workflow identity.
After verification, replace the deployment digest, recreate the container with the same restrictions, reload the artifact and check `/healthz` and known image classifications.
Retain the previous verified digest and model for rollback.
