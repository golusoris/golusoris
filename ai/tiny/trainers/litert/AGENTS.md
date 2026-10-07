<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# ai/tiny/trainers/litert

Keras MobileNet V2 image-classification container entrypoint.

## Contract

- Dataset: staged local tar, tar.gz, tgz, or zip only.
- Extraction: traversal, link, special-file, member, expansion caps.
- Expansion workspace: private temporary directory on `/work/output`, outside
  bounded `/tmp` tmpfs; removed before exit.
- Images: extension and header match; count, bytes, dimensions, pixels,
  channels bounded; two labels and two images per label.
- Network: denied; `pretrained=false`; no remote weight acquisition.
- Conversion: `TFLITE_BUILTINS` only; `TFL3` header; interpreter allocation
  and finite-output invocation before publication.
- Output: `model.tflite`; required `metrics.json` with metrics and training
  label order; atomic bounded writes.

## Change gate

- Run: `PYTHONPATH=ai/tiny python3 -B -m unittest trainers.litert.test_trainer`.
- Image: `scripts/ci/tiny-trainer-smoke.sh litert IMAGE`.
- Vulnerabilities: `bash scripts/ci/tiny-trainer-locks.sh --audit`.
