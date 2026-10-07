<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Tiny trainer images

These two image contexts implement the fixed `/work` contract used by the Go
trainers. They are production training runtimes, not placeholder artifact
writers. Their `--contract-smoke` mode validates container wiring only and
deliberately emits an artifact that must never be served as a model.

No image digest is documented as available yet. Issue #563 stays open until
the publish workflow has produced signed images and a consumer has pinned their
recorded digests. Production callers must set `Options.Image` to an exact
`ghcr.io/...@sha256:<64 hex>` reference; the mutable `DefaultImage` constants
are deprecated and rejected by default.

## Contracts

Both images read `/work/input/config.json` and write fixed outputs under
`/work/output`. Callers configure a canonical `Options.DatasetRoot`; a dataset
must be a local regular file beneath `<DatasetRoot>/<TenantID>` or
`<DatasetRoot>/_default`. The Go layer copies it to `/work/input/dataset`.
Remote dataset URIs fail before a runner starts, so cloud credentials never
cross the dataset boundary.

The images run as UID/GID 65532 with a read-only root and input mount. Docker
runs bound process, temporary-storage, and output-file sizes. Both trainers
deny network access by default. Gemma callers must set `AllowNetwork=true` to
acquire an uncached KerasHub preset; LiteRT never enables network access.
Dataset and output paths reject traversal, symlinks, special files, and
identity changes during open. Image archives also bound member count, expanded
bytes, encoded image size, decoded dimensions, pixel count, and channel count.

### Gemma

The Gemma runtime uses KerasHub 0.32.0 and exports
`adapter.lora.h5` with `Backbone.save_lora_weights`, plus a flat numeric
`metrics.json` object.

| Go base model | KerasHub preset | Dataset |
| --- | --- | --- |
| `gemma3:270m` | `gemma3_270m` | JSONL prompt/response |
| `gemma3:1b` | `gemma3_1b` | JSONL prompt/response |
| `gemma3:4b-text` | `gemma3_4b_text` | JSONL prompt/response |
| `gemma3n:e2b` | `gemma3n_e2b` | JSONL prompt/response |
| `gemma3n:e4b` | `gemma3n_e4b` | JSONL prompt/response |

Each JSONL row defaults to `{"prompt":"...","response":"..."}`. Override
the keys with `Dataset.SchemaHint["prompt_column"]` and
`["response_column"]`. Supported hyperparameters are `epochs`, `batch_size`,
`learning_rate`, `weight_decay`, `lora_rank`, `sequence_length`,
`max_examples`, and `dtype_policy`. Unknown keys fail closed.

KerasHub preset downloads require explicit `Options.AllowNetwork=true` and may
require `KAGGLE_USERNAME` and `KAGGLE_KEY`, or the credentials required by the
selected preset handle. Pass credentials only through the runner environment;
never add them to config JSON.

### LiteRT

The LiteRT runtime pins TensorFlow 2.21.0 and Keras 3.15.1. It trains a Keras
MobileNet V2 image classifier from local data, converts it with builtin LiteRT
ops only, invokes the converted model once, and writes `model.tflite`. Its
sidecar shape is
`{"metrics":{"loss":...},"labels":[...]}`.

| Go base model | Keras backbone | Dataset |
| --- | --- | --- |
| `keras:image/mobilenet-v2` | `keras.applications.MobileNetV2` | image-folder archive |

An image dataset is a `tar`, `tar.gz`, `tgz`, or `zip` with at least two
top-level labels and two valid images per label. Training currently requires
`pretrained=false`; pretrained weights need a separate digest-pinned staging
contract. Deprecated text, MediaPipe, MobileBERT, and EfficientNet-Lite Go
constants remain source-compatible identifiers but fail validation.

## Build and test

Each directory is an independent Docker context. The shared contract is passed
as a named BuildKit context:

```bash
PYTHONDONTWRITEBYTECODE=1 PYTHONPATH=ai/tiny \
  python3 -m unittest discover -s ai/tiny/trainers -p 'test_*.py' -v

docker buildx build --load --platform linux/amd64 \
  --build-context common=ai/tiny/trainers/common \
  -f ai/tiny/trainers/gemma/Dockerfile \
  -t local/tiny-gemma-trainer ai/tiny/trainers/gemma
```

The separate Python ABI and ML-wheel constraints currently limit both release
images to `linux/amd64`. `.github/workflows/tiny-trainer-images.yml` runs on
every pull request and performs a real offline LiteRT train, conversion, and
interpreter invocation. Publishing remains blocked until the repository has a
review-protected `tiny-trainer-publish` environment, a protected
`tiny-trainers-v*.*.*` tag rule, and `TINY_TRAINER_PUBLISH_ENABLED=true`.
After that control-plane setup, publication requires the exact version tag and
event SHA. Every attempt rebuilds and smokes the candidate. A retry reuses an
existing tag only when its config digest matches that tested image and its
signature, build provenance, and SPDX SBOM attestation verify against the exact
workflow, source tag, and source SHA. A new push records Docker's manifest
digest, resolves the tag back to that digest and tested config, then signs and
attests it.

Dependency locks include hashes for every Python distribution. CI regenerates
them and audits both complete locks with a pinned `pip-audit`; findings fail
closed. Renovate owns the Dockerfile frontend, base image, audit tool, and
direct Python pins.

## Upstream authority

The exact mappings follow the current
[KerasHub Gemma 3 API](https://keras.io/keras_hub/api/models/gemma3/gemma3_causal_lm/),
[KerasHub Gemma 3n API](https://keras.io/keras_hub/api/models/gemma3n/gemma3n_causal_lm/),
[KerasHub backbone LoRA API](https://keras.io/keras_hub/api/base_classes/backbone/),
[Keras MobileNet V2 API](https://keras.io/api/applications/mobilenet/#mobilenetv2-function),
and the
[TensorFlow Lite converter API](https://www.tensorflow.org/api_docs/python/tf/lite/TFLiteConverter#from_keras_model).
