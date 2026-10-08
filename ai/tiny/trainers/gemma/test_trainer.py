# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

from __future__ import annotations

import json
import tempfile
from pathlib import Path
from unittest import TestCase, main, mock

from trainers.common import contract
from trainers.gemma import trainer


def valid_config(dataset: Path) -> dict[str, object]:
    return {
        "base_model": "gemma3:270m",
        "modality": "text",
        "task_kind": "generate",
        "dataset_fmt": "jsonl",
        "dataset_uri": dataset.as_uri(),
        "max_dataset_bytes": 1024,
        "hyperparams": {"epochs": 1, "lora_rank": 2, "max_examples": 2},
        "schema_hint": {},
    }


class GemmaTrainerTest(TestCase):
    def test_exact_base_mapping(self) -> None:
        self.assertEqual(trainer.preset_for("gemma3:4b-text"), "gemma3_4b_text")
        with self.assertRaisesRegex(contract.ContractError, "unsupported"):
            trainer.preset_for("gemma3:4b")

    def test_load_examples_positive_and_boundary(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            dataset = Path(temporary) / "data.jsonl"
            dataset.write_text(
                '{"prompt":"one","response":"first"}\n'
                '{"prompt":"two","response":"second"}\n',
                encoding="utf-8",
            )
            prompts, responses = trainer.load_examples(dataset, {}, 2)
            self.assertEqual(prompts, ["one", "two"])
            self.assertEqual(responses, ["first", "second"])
            with self.assertRaisesRegex(contract.ContractError, "max_examples=1"):
                trainer.load_examples(dataset, {}, 1)

    def test_load_examples_rejects_missing_response(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            dataset = Path(temporary) / "data.jsonl"
            dataset.write_text(json.dumps({"prompt": "one"}) + "\n", encoding="utf-8")
            with self.assertRaisesRegex(contract.ContractError, "no response"):
                trainer.load_examples(dataset, {}, 2)

    def test_load_examples_enforces_record_byte_cap_at_exact_boundary(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            dataset = Path(temporary) / "data.jsonl"
            encoded = b'{"prompt":"one","response":"first"}\n'
            dataset.write_bytes(encoded)
            with mock.patch.object(trainer, "MAX_RECORD_BYTES", len(encoded)):
                prompts, _ = trainer.load_examples(dataset, {}, 1)
                self.assertEqual(prompts, ["one"])
            with mock.patch.object(trainer, "MAX_RECORD_BYTES", len(encoded) - 1):
                with self.assertRaisesRegex(contract.ContractError, "row 1 exceeds"):
                    trainer.load_examples(dataset, {}, 1)

    def test_load_examples_enforces_aggregate_text_cap_at_exact_boundary(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            dataset = Path(temporary) / "data.jsonl"
            dataset.write_text('{"prompt":"one","response":"four"}\n', encoding="utf-8")
            with mock.patch.object(trainer, "MAX_TEXT_BYTES", 7):
                prompts, responses = trainer.load_examples(dataset, {}, 1)
                self.assertEqual((prompts, responses), (["one"], ["four"]))
            with mock.patch.object(trainer, "MAX_TEXT_BYTES", 6):
                with self.assertRaisesRegex(contract.ContractError, "text exceeds"):
                    trainer.load_examples(dataset, {}, 1)

    def test_config_rejects_unknown_hyperparameter_and_boolean_integer(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            dataset = Path(temporary) / "data.jsonl"
            config = valid_config(dataset)
            config["hyperparams"] = {"surprise": 1}
            with self.assertRaisesRegex(contract.ContractError, "surprise"):
                trainer.validate_config(config)
            config["hyperparams"] = {"epochs": True}
            with self.assertRaisesRegex(contract.ContractError, "epochs"):
                trainer.validate_config(config)

    def test_contract_smoke_emits_fixed_outputs(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            dataset = root / "data.jsonl"
            dataset.write_text('{"prompt":"one","response":"first"}\n', encoding="utf-8")
            output = root / "output"
            trainer.contract_smoke(valid_config(dataset), dataset, output)
            self.assertGreater((output / trainer.ARTIFACT_NAME).stat().st_size, 0)
            metrics = json.loads((output / trainer.METRICS_NAME).read_text(encoding="utf-8"))
            self.assertEqual(metrics, {"contract_smoke": 1.0, "examples": 1.0})


if __name__ == "__main__":
    main()
