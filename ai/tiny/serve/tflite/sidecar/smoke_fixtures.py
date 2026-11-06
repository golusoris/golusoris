# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""Build deterministic trainer artifacts and known images for the Docker smoke."""

from __future__ import annotations

import io
import json
import sys
import tarfile
from pathlib import Path

import numpy as np
import tensorflow as tf
from trainers.litert.trainer import train


def write_dataset(root: Path) -> Path:
    """Pack two visually separable image folders with small seeded noise."""
    rng = np.random.default_rng(1337)
    archive = root / "images.tar"
    with tarfile.open(archive, "w") as writer:
        for label, colour in (("blue", (8, 8, 240)), ("red", (240, 8, 8))):
            pattern = np.broadcast_to(np.array(colour), (160, 160, 3)).copy()
            stripes = np.arange(160) % 32 < 8
            if label == "blue":
                pattern[:, stripes] = (232, 232, 232)
            else:
                pattern[stripes, :] = (232, 232, 232)
            for index in range(32):
                pixels = np.clip(
                    pattern + rng.integers(-8, 9, size=(160, 160, 3)), 0, 255
                ).astype(np.uint8)
                payload = tf.io.encode_png(pixels).numpy()
                member = tarfile.TarInfo(f"{label}/{index:03d}.png")
                member.size = len(payload)
                writer.addfile(member, io.BytesIO(payload))
            # Held-out stripes do not reuse training bytes.
            pixels = pattern.astype(np.uint8)
            (root / f"{label}.png").write_bytes(tf.io.encode_png(pixels).numpy())
    return archive


def report_scores(root: Path, labels: list[str]) -> None:
    """Measure exported probabilities before the Go Predictor assertions."""
    interpreter = tf.lite.Interpreter(model_path=str(root / "models/model.tflite"))
    interpreter.allocate_tensors()
    for label in labels:
        pixels = tf.io.decode_png(
            tf.io.read_file(str(root / f"{label}.png")), channels=3
        )
        interpreter.set_tensor(
            interpreter.get_input_details()[0]["index"],
            tf.cast(pixels[None, ...], tf.float32).numpy(),
        )
        interpreter.invoke()
        scores = interpreter.get_tensor(interpreter.get_output_details()[0]["index"])[0]
        measured = dict(zip(labels, scores.tolist(), strict=True))
        print(f"trainer export: expected={label} scores={measured}")


def main(root: Path) -> None:
    """Train the real MobileNetV2 path with locked dependencies and fixed settings."""
    tf.config.threading.set_inter_op_parallelism_threads(2)
    tf.config.threading.set_intra_op_parallelism_threads(4)
    tf.keras.utils.set_random_seed(1337)
    tf.config.experimental.enable_op_determinism()
    archive = write_dataset(root)
    config = {
        "base_model": "keras:image/mobilenet-v2",
        "modality": "image",
        "task_kind": "classify",
        "dataset_fmt": "tar",
        "dataset_uri": archive.as_uri(),
        "max_dataset_bytes": 16 << 20,
        "hyperparams": {
            "epochs": 1000,
            "batch_size": 8,
            "learning_rate": 0.0001,
            "validation_fraction": 0.25,
            "do_data_augmentation": False,
            "pretrained": False,
        },
    }
    train(config, archive, root / "models", root / "work")
    labels = json.loads((root / "models/metrics.json").read_text())["labels"]
    (root / "labels").write_text(",".join(labels))
    report_scores(root, labels)


if __name__ == "__main__":
    main(Path(sys.argv[1]))
