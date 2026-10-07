# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""Train supported KerasHub Gemma presets and emit LoRA weights."""

from __future__ import annotations

import argparse
import importlib
import json
import math
import sys
import tempfile
from collections.abc import Mapping
from pathlib import Path
from typing import Any

from trainers.common import contract

PRESETS = {
    "gemma3:270m": "gemma3_270m",
    "gemma3:1b": "gemma3_1b",
    "gemma3:4b-text": "gemma3_4b_text",
    "gemma3n:e2b": "gemma3n_e2b",
    "gemma3n:e4b": "gemma3n_e4b",
}
ALLOWED_HYPERPARAMS = {
    "batch_size",
    "dtype_policy",
    "epochs",
    "learning_rate",
    "lora_rank",
    "max_examples",
    "sequence_length",
    "weight_decay",
}
MAX_EXAMPLES = 100_000
MAX_RECORD_BYTES = 1 << 20
MAX_TEXT_BYTES = 64 << 20
ARTIFACT_NAME = "adapter.lora.h5"
METRICS_NAME = "metrics.json"


def preset_for(base_model: str) -> str:
    """Map the public Go contract to one exact KerasHub preset."""
    try:
        return PRESETS[base_model]
    except KeyError as exc:
        raise contract.ContractError(f"unsupported Gemma base_model: {base_model}") from exc


def validate_config(config: Mapping[str, Any]) -> dict[str, Any]:
    """Validate the task and bounded hyperparameter surface."""
    if contract.required_string(config, "modality") != "text":
        raise contract.ContractError("Gemma modality must be text")
    if contract.required_string(config, "task_kind") != "generate":
        raise contract.ContractError("Gemma task_kind must be generate")
    if contract.required_string(config, "dataset_fmt") != "jsonl":
        raise contract.ContractError("Gemma dataset_fmt must be jsonl")
    preset_for(contract.required_string(config, "base_model"))
    raw = contract.object_field(config, "hyperparams")
    unknown = sorted(set(raw) - ALLOWED_HYPERPARAMS)
    if unknown:
        raise contract.ContractError(f"unsupported Gemma hyperparameter: {unknown[0]}")
    values = {
        "epochs": _integer(raw, "epochs", 1, 1, 100),
        "batch_size": _integer(raw, "batch_size", 1, 1, 1024),
        "learning_rate": _number(raw, "learning_rate", 5e-5, 1e-8, 1.0),
        "weight_decay": _number(raw, "weight_decay", 0.01, 0.0, 1.0),
        "lora_rank": _integer(raw, "lora_rank", 4, 1, 256),
        "sequence_length": _integer(raw, "sequence_length", 512, 8, 32_768),
        "max_examples": _integer(raw, "max_examples", 100_000, 1, MAX_EXAMPLES),
    }
    dtype_policy = raw.get("dtype_policy", "bfloat16")
    if dtype_policy not in {"float32", "float16", "bfloat16", "mixed_float16", "mixed_bfloat16"}:
        raise contract.ContractError("dtype_policy is not supported")
    values["dtype_policy"] = dtype_policy
    return values


def load_examples(
    path: Path, schema_hint: Mapping[str, Any], max_examples: int
) -> tuple[list[str], list[str]]:
    """Load the bounded JSONL prompt/response shape."""
    prompt_key = _schema_column(schema_hint, "prompt_column", "prompt")
    response_key = _schema_column(schema_hint, "response_column", "response")
    prompts: list[str] = []
    responses: list[str] = []
    text_bytes = 0
    with path.open("rb") as handle:
        for row_index in range(1, max_examples + 2):
            encoded = handle.readline(MAX_RECORD_BYTES + 1)
            if not encoded:
                break
            if len(encoded) > MAX_RECORD_BYTES:
                raise contract.ContractError(
                    f"JSONL row {row_index} exceeds {MAX_RECORD_BYTES} bytes"
                )
            if row_index > max_examples:
                raise contract.ContractError(f"dataset exceeds max_examples={max_examples}")
            try:
                line = encoded.decode("utf-8")
            except UnicodeError as exc:
                raise contract.ContractError("JSONL dataset must be UTF-8") from exc
            prompt, response = _parse_example(line, row_index, prompt_key, response_key)
            text_bytes += len(prompt.encode("utf-8")) + len(response.encode("utf-8"))
            if text_bytes > MAX_TEXT_BYTES:
                raise contract.ContractError(
                    f"JSONL prompt and response text exceeds {MAX_TEXT_BYTES} bytes"
                )
            prompts.append(prompt)
            responses.append(response)
    if not prompts:
        raise contract.ContractError("JSONL dataset is empty")
    return prompts, responses


def _schema_column(schema_hint: Mapping[str, Any], name: str, default: str) -> str:
    value = schema_hint.get(name, default)
    if not isinstance(value, str) or not value:
        raise contract.ContractError(f"schema_hint.{name} must be a non-empty string")
    return value


def _parse_example(line: str, row_index: int, prompt_key: str, response_key: str) -> tuple[str, str]:
    try:
        row = json.loads(line)
    except ValueError as exc:
        raise contract.ContractError(f"JSONL row {row_index} is invalid JSON") from exc
    if not isinstance(row, dict):
        raise contract.ContractError(f"JSONL row {row_index} must be an object")
    prompt = row.get(prompt_key)
    response = row.get(response_key)
    if not isinstance(prompt, str) or not prompt.strip():
        raise contract.ContractError(f"JSONL row {row_index} has no prompt")
    if not isinstance(response, str) or not response.strip():
        raise contract.ContractError(f"JSONL row {row_index} has no response")
    return prompt, response


def train(config: Mapping[str, Any], dataset: Path, output: Path) -> None:
    """Fine-tune one exact KerasHub preset and export its LoRA weights."""
    hyperparams = validate_config(config)
    prompts, responses = load_examples(
        dataset, contract.object_field(config, "schema_hint"), hyperparams["max_examples"]
    )
    keras = importlib.import_module("keras")
    keras_hub = importlib.import_module("keras_hub")
    keras.config.set_dtype_policy(hyperparams["dtype_policy"])
    model = keras_hub.models.CausalLM.from_preset(
        preset_for(contract.required_string(config, "base_model"))
    )
    model.backbone.enable_lora(rank=hyperparams["lora_rank"])
    model.preprocessor.sequence_length = hyperparams["sequence_length"]
    optimizer = keras.optimizers.AdamW(
        learning_rate=hyperparams["learning_rate"],
        weight_decay=hyperparams["weight_decay"],
    )
    model.compile(
        optimizer=optimizer,
        loss=keras.losses.SparseCategoricalCrossentropy(from_logits=True),
        weighted_metrics=[keras.metrics.SparseCategoricalAccuracy()],
    )
    history = model.fit(
        {"prompts": prompts, "responses": responses},
        batch_size=hyperparams["batch_size"],
        epochs=hyperparams["epochs"],
    )
    output.mkdir(mode=0o755, parents=True, exist_ok=True)
    artifact = output / ARTIFACT_NAME
    model.backbone.save_lora_weights(str(artifact))
    if not artifact.is_file() or artifact.stat().st_size == 0:
        raise RuntimeError("KerasHub did not write a non-empty LoRA artifact")
    metrics = _final_metrics(history.history)
    metrics["examples"] = float(len(prompts))
    contract.atomic_write_json(output / METRICS_NAME, metrics)


def contract_smoke(config: Mapping[str, Any], dataset: Path, output: Path) -> None:
    """Validate wiring without importing ML frameworks or training a model."""
    hyperparams = validate_config(config)
    prompts, _ = load_examples(
        dataset, contract.object_field(config, "schema_hint"), hyperparams["max_examples"]
    )
    output.mkdir(mode=0o755, parents=True, exist_ok=True)
    (output / ARTIFACT_NAME).write_bytes(b"GOLUSORIS-CONTRACT-SMOKE\n")
    contract.atomic_write_json(
        output / METRICS_NAME, {"contract_smoke": 1.0, "examples": float(len(prompts))}
    )


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


def _final_metrics(history: Mapping[str, Any]) -> dict[str, float]:
    metrics: dict[str, float] = {}
    for name in sorted(history):
        values = history[name]
        if not isinstance(name, str) or not isinstance(values, (list, tuple)) or not values:
            continue
        value = float(values[-1])
        if math.isfinite(value):
            metrics[name] = value
    return metrics


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
        with tempfile.TemporaryDirectory(prefix="tiny-gemma-") as temporary:
            dataset = contract.materialize_dataset(config, Path(temporary))
            action = contract_smoke if args.contract_smoke else train
            action(config, dataset, contract.OUTPUT_ROOT)
    except contract.ContractError as exc:
        print(f"tiny-gemma-trainer: contract: {exc}", file=sys.stderr)
        return 2
    except Exception as exc:
        print(f"tiny-gemma-trainer: training failed: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
