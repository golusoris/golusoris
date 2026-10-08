# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""Train supported TensorFlow classifiers and emit LiteRT artifacts."""

from __future__ import annotations

import argparse
import importlib
import math
import struct
import sys
import tempfile
from collections.abc import Mapping
from pathlib import Path
from typing import Any

from trainers.common import contract

IMAGE_MODELS = {"keras:image/mobilenet-v2"}
UNSUPPORTED_LEGACY_MODELS = {
    "tensorflow:text/average-word-embedding": "LiteRT training is image-only",
    "mediapipe:text/average-word-embedding": "LiteRT training is image-only",
    "mediapipe:text/mobilebert": "MobileBERT has no supported Keras 3 trainer",
    "mediapipe:image/mobilenet-v2": "use keras:image/mobilenet-v2",
    "mediapipe:image/mobilenet-v2-keras": "use keras:image/mobilenet-v2",
    "mediapipe:image/efficientnet-lite0": "EfficientNet-Lite0 is not Keras EfficientNetB0",
    "mediapipe:image/efficientnet-lite2": "EfficientNet-Lite2 is not Keras EfficientNetB2",
    "mediapipe:image/efficientnet-lite4": "EfficientNet-Lite4 is not Keras EfficientNetB4",
}
COMMON_HYPERPARAMS = {"batch_size", "epochs", "learning_rate", "validation_fraction"}
IMAGE_HYPERPARAMS = COMMON_HYPERPARAMS | {
    "do_data_augmentation",
    "do_fine_tuning",
    "pretrained",
}
MAX_IMAGE_FILES = 100_000
MAX_IMAGE_BYTES = 64 << 20
MAX_IMAGE_DIMENSION = 8192
MAX_IMAGE_PIXELS = 4096 * 4096
MAX_JPEG_HEADER_BYTES = 1 << 20
MAX_JPEG_SEGMENTS = 4096
IMAGE_SUFFIXES = {".bmp", ".gif", ".jpeg", ".jpg", ".png"}
ARTIFACT_NAME = "model.tflite"
METRICS_NAME = "metrics.json"


def validate_config(config: Mapping[str, Any]) -> dict[str, Any]:
    """Validate exact TensorFlow task, format, model, and hyperparameters."""
    if contract.required_string(config, "task_kind") != "classify":
        raise contract.ContractError("LiteRT task_kind must be classify")
    modality = contract.required_string(config, "modality")
    base_model = contract.required_string(config, "base_model")
    dataset_fmt = contract.required_string(config, "dataset_fmt")
    if base_model in UNSUPPORTED_LEGACY_MODELS:
        raise contract.ContractError(
            f"unsupported legacy base_model {base_model}: {UNSUPPORTED_LEGACY_MODELS[base_model]}"
        )
    if modality == "image":
        if base_model not in IMAGE_MODELS:
            raise contract.ContractError(f"unsupported image base_model: {base_model}")
        if dataset_fmt not in {"tar", "tar.gz", "tgz", "zip"}:
            raise contract.ContractError("image dataset_fmt must be tar, tar.gz, tgz, or zip")
        allowed = IMAGE_HYPERPARAMS
    else:
        raise contract.ContractError("LiteRT modality must be image")
    raw = contract.object_field(config, "hyperparams")
    unknown = sorted(set(raw) - allowed)
    if unknown:
        raise contract.ContractError(f"unsupported LiteRT hyperparameter: {unknown[0]}")
    values: dict[str, Any] = {
        "epochs": _integer(raw, "epochs", 10, 1, 1000),
        "batch_size": _integer(raw, "batch_size", 8, 1, 4096),
        "learning_rate": _number(raw, "learning_rate", 0.001, 1e-8, 1.0),
        "validation_fraction": _number(raw, "validation_fraction", 0.2, 0.01, 0.5),
    }
    values["do_data_augmentation"] = _boolean(raw, "do_data_augmentation", True)
    values["do_fine_tuning"] = _boolean(raw, "do_fine_tuning", False)
    values["pretrained"] = _boolean(raw, "pretrained", False)
    if values["pretrained"]:
        raise contract.ContractError("pretrained=true requires staged digest-pinned weights")
    if values["do_fine_tuning"]:
        raise contract.ContractError("do_fine_tuning=true requires pretrained weights")
    return values


def inspect_image_folder(root: Path) -> list[str]:
    """Validate the one-directory-per-label image dataset shape."""
    labels: list[str] = []
    file_count = 0
    for child in sorted(root.iterdir()):
        if child.is_symlink():
            raise contract.ContractError("image folder must not contain links")
        if not child.is_dir():
            continue
        label_files = _inspect_label_folder(child, MAX_IMAGE_FILES - file_count)
        file_count += label_files
        if label_files == 1:
            raise contract.ContractError("each image label must contain at least two images")
        if label_files >= 2:
            labels.append(child.name)
    if len(labels) < 2:
        raise contract.ContractError("image folder must contain at least two non-empty label directories")
    return labels


def _inspect_label_folder(root: Path, max_files: int) -> int:
    file_count = 0
    for candidate in root.rglob("*"):
        if candidate.is_symlink():
            raise contract.ContractError("image folder must not contain links")
        if candidate.is_file():
            file_count += 1
            if file_count > max_files:
                raise contract.ContractError(f"image folder exceeds {MAX_IMAGE_FILES} files")
            _validate_image_file(candidate)
    return file_count


def _validate_image_file(path: Path) -> None:
    suffix = path.suffix.lower()
    if suffix not in IMAGE_SUFFIXES:
        raise contract.ContractError(f"unsupported image extension: {path.suffix or '<none>'}")
    size = path.stat().st_size
    if size <= 0 or size > MAX_IMAGE_BYTES:
        raise contract.ContractError(f"image file exceeds {MAX_IMAGE_BYTES} encoded bytes")
    if suffix == ".png":
        geometry = _png_geometry(path)
    elif suffix == ".gif":
        geometry = _gif_geometry(path)
    elif suffix == ".bmp":
        geometry = _bmp_geometry(path)
    else:
        geometry = _jpeg_geometry(path)
    _validate_geometry(*geometry)


def _png_geometry(path: Path) -> tuple[int, int, int]:
    with path.open("rb") as handle:
        header = handle.read(29)
    if (
        len(header) != 29
        or header[:8] != b"\x89PNG\r\n\x1a\n"
        or header[8:12] != b"\x00\x00\x00\r"
        or header[12:16] != b"IHDR"
        or header[26:28] != b"\x00\x00"
        or header[28] > 1
    ):
        raise contract.ContractError(f"image file has invalid PNG header: {path.name}")
    width, height = struct.unpack(">II", header[16:24])
    channels = {0: 1, 2: 3, 3: 3, 4: 2, 6: 4}.get(header[25], 0)
    return width, height, channels


def _gif_geometry(path: Path) -> tuple[int, int, int]:
    with path.open("rb") as handle:
        header = handle.read(10)
    if len(header) != 10 or header[:6] not in {b"GIF87a", b"GIF89a"}:
        raise contract.ContractError(f"image file has invalid GIF header: {path.name}")
    width, height = struct.unpack("<HH", header[6:10])
    return width, height, 3


def _bmp_geometry(path: Path) -> tuple[int, int, int]:
    with path.open("rb") as handle:
        header = handle.read(34)
    if len(header) != 34 or header[:2] != b"BM" or struct.unpack("<I", header[14:18])[0] < 40:
        raise contract.ContractError(f"image file has invalid BMP header: {path.name}")
    width, height = struct.unpack("<ii", header[18:26])
    bits_per_pixel = struct.unpack("<H", header[28:30])[0]
    compression = struct.unpack("<I", header[30:34])[0]
    if compression != 0 or bits_per_pixel not in {24, 32}:
        raise contract.ContractError(f"image file uses unsupported BMP encoding: {path.name}")
    return width, abs(height), bits_per_pixel // 8


def _jpeg_geometry(path: Path) -> tuple[int, int, int]:
    with path.open("rb") as handle:
        if handle.read(2) != b"\xff\xd8":
            raise contract.ContractError(f"image file has invalid JPEG header: {path.name}")
        scanned = 2
        for _ in range(MAX_JPEG_SEGMENTS):
            marker, scanned = _next_jpeg_marker(handle, scanned, path.name)
            if marker in {0x01, *range(0xD0, 0xDA)}:
                continue
            length_bytes = handle.read(2)
            scanned += 2
            if len(length_bytes) != 2:
                break
            segment_size = int.from_bytes(length_bytes, "big")
            if segment_size < 2 or scanned + segment_size - 2 > MAX_JPEG_HEADER_BYTES:
                break
            if marker in {0xC0, 0xC1, 0xC2, 0xC3, 0xC5, 0xC6, 0xC7, 0xC9, 0xCA, 0xCB, 0xCD, 0xCE, 0xCF}:
                payload = handle.read(6)
                if segment_size < 8 or len(payload) != 6:
                    break
                return (
                    int.from_bytes(payload[3:5], "big"),
                    int.from_bytes(payload[1:3], "big"),
                    payload[5],
                )
            handle.seek(segment_size - 2, 1)
            scanned += segment_size - 2
    raise contract.ContractError(f"image file lacks bounded JPEG dimensions: {path.name}")


def _next_jpeg_marker(handle: Any, scanned: int, name: str) -> tuple[int, int]:
    while scanned < MAX_JPEG_HEADER_BYTES:
        prefix = handle.read(1)
        scanned += 1
        if not prefix:
            break
        if prefix != b"\xff":
            continue
        marker = handle.read(1)
        scanned += 1
        while marker == b"\xff" and scanned < MAX_JPEG_HEADER_BYTES:
            marker = handle.read(1)
            scanned += 1
        if marker and marker != b"\x00":
            return marker[0], scanned
    raise contract.ContractError(f"image file lacks bounded JPEG marker: {name}")


def _validate_geometry(width: int, height: int, channels: int) -> None:
    if width <= 0 or height <= 0 or width > MAX_IMAGE_DIMENSION or height > MAX_IMAGE_DIMENSION:
        raise contract.ContractError("image dimensions exceed the supported boundary")
    if width * height > MAX_IMAGE_PIXELS:
        raise contract.ContractError(f"decoded image exceeds {MAX_IMAGE_PIXELS} pixels")
    if channels < 1 or channels > 4:
        raise contract.ContractError("decoded image channel count must be in [1, 4]")


def train(config: Mapping[str, Any], dataset: Path, output: Path, workspace: Path) -> None:
    """Train one supported image classifier and export a `.tflite` model."""
    hyperparams = validate_config(config)
    output.mkdir(mode=0o755, parents=True, exist_ok=True)
    metrics, labels = _train_image(config, dataset, output, workspace, hyperparams)
    artifact = output / ARTIFACT_NAME
    if not artifact.is_file() or artifact.stat().st_size == 0:
        raise RuntimeError("TensorFlow did not write a non-empty LiteRT artifact")
    contract.atomic_write_json(output / METRICS_NAME, {"metrics": metrics, "labels": labels})


def contract_smoke(
    config: Mapping[str, Any], dataset: Path, output: Path, workspace: Path
) -> None:
    """Validate wiring without importing TensorFlow or training a model."""
    validate_config(config)
    images = workspace / "images"
    contract.safe_extract_archive(
        dataset,
        images,
        contract.required_string(config, "dataset_fmt"),
        int(config["max_dataset_bytes"]),
    )
    labels = inspect_image_folder(images)
    output.mkdir(mode=0o755, parents=True, exist_ok=True)
    (output / ARTIFACT_NAME).write_bytes(b"\x08\x00\x00\x00TFL3GOLUSORIS-CONTRACT-SMOKE\n")
    contract.atomic_write_json(
        output / METRICS_NAME,
        {"metrics": {"contract_smoke": 1.0}, "labels": labels},
    )


def _train_image(
    config: Mapping[str, Any],
    dataset: Path,
    output: Path,
    workspace: Path,
    hyperparams: Mapping[str, Any],
) -> tuple[dict[str, float], list[str]]:
    images = workspace / "images"
    contract.safe_extract_archive(
        dataset,
        images,
        contract.required_string(config, "dataset_fmt"),
        int(config["max_dataset_bytes"]),
    )
    labels = inspect_image_folder(images)
    tensorflow = importlib.import_module("tensorflow")
    train_data, validation_data = _load_image_data(tensorflow, images, hyperparams)
    model = _build_image_model(tensorflow, len(labels), hyperparams)
    model.fit(
        train_data,
        validation_data=validation_data,
        epochs=int(hyperparams["epochs"]),
        verbose=2,
    )
    evaluation = model.evaluate(validation_data, verbose=0, return_dict=True)
    _export_tflite(tensorflow, model, output / ARTIFACT_NAME)
    return _evaluation_metrics(evaluation), labels


def _load_image_data(
    tensorflow: Any, images: Path, hyperparams: Mapping[str, Any]
) -> tuple[Any, Any]:
    dataset_options = {
        "directory": str(images),
        "validation_split": float(hyperparams["validation_fraction"]),
        "seed": 1_337,
        "image_size": (160, 160),
        "batch_size": int(hyperparams["batch_size"]),
        "label_mode": "int",
    }
    train_data = tensorflow.keras.utils.image_dataset_from_directory(
        subset="training", shuffle=True, **dataset_options
    )
    validation_data = tensorflow.keras.utils.image_dataset_from_directory(
        subset="validation", shuffle=False, **dataset_options
    )
    return train_data, validation_data


def _build_image_model(
    tensorflow: Any, label_count: int, hyperparams: Mapping[str, Any]
) -> Any:
    inputs = tensorflow.keras.Input(shape=(160, 160, 3), name="image")
    features = inputs
    if hyperparams["do_data_augmentation"]:
        augmentation = tensorflow.keras.Sequential(
            [
                tensorflow.keras.layers.RandomFlip("horizontal"),
                tensorflow.keras.layers.RandomRotation(0.05),
            ],
            name="augmentation",
        )
        features = augmentation(features)
    features = tensorflow.keras.layers.Rescaling(
        scale=1.0 / 127.5, offset=-1.0, name="mobilenet_scaling"
    )(features)
    backbone = tensorflow.keras.applications.MobileNetV2(
        input_shape=(160, 160, 3),
        include_top=False,
        weights=None,
        pooling="avg",
    )
    backbone.trainable = True
    features = backbone(features)
    outputs = tensorflow.keras.layers.Dense(
        label_count, activation="softmax", name="scores"
    )(features)
    model = tensorflow.keras.Model(inputs, outputs, name="mobilenet_v2_classifier")
    model.compile(
        optimizer=tensorflow.keras.optimizers.Adam(hyperparams["learning_rate"]),
        loss="sparse_categorical_crossentropy",
        metrics=["accuracy"],
    )
    return model


def _export_tflite(tensorflow: Any, model: Any, artifact: Path) -> None:
    """Convert with builtin ops and prove one interpreter invocation."""
    converter = tensorflow.lite.TFLiteConverter.from_keras_model(model)
    converter.target_spec.supported_ops = [tensorflow.lite.OpsSet.TFLITE_BUILTINS]
    payload = converter.convert()
    if len(payload) < 8 or payload[4:8] != b"TFL3":
        raise RuntimeError("TensorFlow emitted an invalid LiteRT FlatBuffer")
    interpreter = tensorflow.lite.Interpreter(model_content=payload)
    interpreter.allocate_tensors()
    input_detail = interpreter.get_input_details()[0]
    input_shape = [max(1, int(dimension)) for dimension in input_detail["shape"]]
    sample = tensorflow.zeros(
        input_shape, dtype=tensorflow.as_dtype(input_detail["dtype"])
    ).numpy()
    interpreter.set_tensor(input_detail["index"], sample)
    interpreter.invoke()
    for output_detail in interpreter.get_output_details():
        result = tensorflow.convert_to_tensor(interpreter.get_tensor(output_detail["index"]))
        if not bool(tensorflow.reduce_all(tensorflow.math.is_finite(result)).numpy()):
            raise RuntimeError("LiteRT interpreter produced non-finite output")
    artifact.write_bytes(payload)


def _evaluation_metrics(evaluation: Any) -> dict[str, float]:
    if isinstance(evaluation, Mapping):
        return {
            str(name): float(value)
            for name, value in evaluation.items()
            if math.isfinite(float(value))
        }
    values = evaluation if isinstance(evaluation, (list, tuple)) else [evaluation]
    names = ("loss", "accuracy")
    metrics: dict[str, float] = {}
    for index in range(min(len(values), len(names))):
        value = float(values[index])
        if math.isfinite(value):
            metrics[names[index]] = value
    return metrics


def _integer(
    values: Mapping[str, Any], name: str, default: int, minimum: int, maximum: int
) -> int:
    value = values.get(name, default)
    if isinstance(value, bool) or not isinstance(value, int) or not minimum <= value <= maximum:
        raise contract.ContractError(f"{name} must be an integer in [{minimum}, {maximum}]")
    return value


def _number(
    values: Mapping[str, Any], name: str, default: float, minimum: float, maximum: float
) -> float:
    value = values.get(name, default)
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise contract.ContractError(f"{name} must be a number")
    number = float(value)
    if not math.isfinite(number) or not minimum <= number <= maximum:
        raise contract.ContractError(f"{name} must be finite in [{minimum}, {maximum}]")
    return number


def _boolean(values: Mapping[str, Any], name: str, default: bool) -> bool:
    value = values.get(name, default)
    if not isinstance(value, bool):
        raise contract.ContractError(f"{name} must be boolean")
    return value


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--contract-smoke",
        action="store_true",
        help="validate container I/O only; emitted artifact is not a model",
    )
    args = parser.parse_args(argv)
    try:
        config = contract.load_config()
        with tempfile.TemporaryDirectory(
            prefix=".tiny-litert-", dir=contract.OUTPUT_ROOT
        ) as temporary:
            workspace = Path(temporary)
            dataset = contract.materialize_dataset(config, workspace / "fetch")
            action = contract_smoke if args.contract_smoke else train
            action(config, dataset, contract.OUTPUT_ROOT, workspace)
    except contract.ContractError as exc:
        print(f"tiny-litert-trainer: contract: {exc}", file=sys.stderr)
        return 2
    except Exception as exc:
        print(f"tiny-litert-trainer: training failed: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
