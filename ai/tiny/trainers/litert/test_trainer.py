# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

from __future__ import annotations

import base64
import io
import json
import tarfile
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from trainers.common import contract
from trainers.litert import trainer

PNG_BYTES = base64.b64decode(
    "iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAIAAAD91JpzAAAAE0lEQVR4nGP4z8Dwn4EBTPxnAAAd8AP92a2PFgAAAABJRU5ErkJggg=="
)


def image_config(dataset: Path) -> dict[str, object]:
    return {
        "base_model": "keras:image/mobilenet-v2",
        "modality": "image",
        "task_kind": "classify",
        "dataset_fmt": "tar",
        "dataset_uri": dataset.as_uri(),
        "max_dataset_bytes": 4096,
        "hyperparams": {"epochs": 1, "pretrained": False},
    }


class LiteRTTrainerTest(unittest.TestCase):
    def test_main_places_expansion_workspace_on_output_mount(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            output = root / "output"
            output.mkdir()
            dataset = root / "images.tar"
            observed: list[Path] = []

            def capture_workspace(
                _config: object,
                _dataset: Path,
                _output: Path,
                workspace: Path,
            ) -> None:
                observed.append(workspace)

            with (
                mock.patch.object(contract, "OUTPUT_ROOT", output),
                mock.patch.object(contract, "load_config", return_value=image_config(dataset)),
                mock.patch.object(contract, "materialize_dataset", return_value=dataset),
                mock.patch.object(trainer, "contract_smoke", side_effect=capture_workspace),
            ):
                self.assertEqual(trainer.main(["--contract-smoke"]), 0)

            self.assertEqual(len(observed), 1)
            self.assertEqual(observed[0].parent, output)

    def test_config_accepts_exact_image_mapping_and_rejects_other_modalities(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            dataset = Path(temporary) / "images.tar"
            config = image_config(dataset)
            self.assertEqual(trainer.validate_config(config)["epochs"], 1)
            config["modality"] = "audio"
            with self.assertRaisesRegex(contract.ContractError, "must be image"):
                trainer.validate_config(config)

    def test_config_rejects_removed_architectures(self) -> None:
        removed = {
            "tensorflow:text/average-word-embedding",
            "mediapipe:text/average-word-embedding",
            "mediapipe:text/mobilebert",
            "mediapipe:image/mobilenet-v2",
            "mediapipe:image/mobilenet-v2-keras",
            "mediapipe:image/efficientnet-lite0",
            "mediapipe:image/efficientnet-lite2",
            "mediapipe:image/efficientnet-lite4",
        }
        with tempfile.TemporaryDirectory() as temporary:
            dataset = Path(temporary) / "images.tar"
            for base_model in removed:
                config = image_config(dataset)
                config["base_model"] = base_model
                with self.assertRaises(contract.ContractError, msg=base_model):
                    trainer.validate_config(config)

    def test_config_rejects_unstaged_weights_and_fine_tuning(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            dataset = Path(temporary) / "images.tar"
            config = image_config(dataset)
            config["hyperparams"] = {"pretrained": True}
            with self.assertRaisesRegex(contract.ContractError, "digest-pinned weights"):
                trainer.validate_config(config)
            config["hyperparams"] = {"do_fine_tuning": True}
            with self.assertRaisesRegex(contract.ContractError, "pretrained weights"):
                trainer.validate_config(config)

    def test_image_folder_boundary_two_labels(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "a").mkdir()
            (root / "b").mkdir()
            for name in ("a/one.png", "a/two.png", "b/one.png", "b/two.png"):
                (root / name).write_bytes(PNG_BYTES)
            self.assertEqual(trainer.inspect_image_folder(root), ["a", "b"])

    def test_image_folder_file_count_boundary(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "a").mkdir()
            (root / "b").mkdir()
            for name in ("a/one.png", "a/two.png", "b/one.png", "b/two.png"):
                (root / name).write_bytes(PNG_BYTES)
            with mock.patch.object(trainer, "MAX_IMAGE_FILES", 4):
                self.assertEqual(trainer.inspect_image_folder(root), ["a", "b"])
            (root / "b/three.png").write_bytes(PNG_BYTES)
            with mock.patch.object(trainer, "MAX_IMAGE_FILES", 4):
                with self.assertRaisesRegex(contract.ContractError, "exceeds 4 files"):
                    trainer.inspect_image_folder(root)

    def test_image_encoded_size_boundary(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            image = Path(temporary) / "one.png"
            image.write_bytes(PNG_BYTES)
            with mock.patch.object(trainer, "MAX_IMAGE_BYTES", len(PNG_BYTES)):
                trainer._validate_image_file(image)
            with mock.patch.object(trainer, "MAX_IMAGE_BYTES", len(PNG_BYTES) - 1):
                with self.assertRaisesRegex(contract.ContractError, "encoded bytes"):
                    trainer._validate_image_file(image)

    def test_image_geometry_and_channel_boundaries(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            image = Path(temporary) / "one.png"
            payload = bytearray(PNG_BYTES)
            payload[16:20] = (8).to_bytes(4, "big")
            payload[20:24] = (8).to_bytes(4, "big")
            image.write_bytes(payload)
            with mock.patch.object(trainer, "MAX_IMAGE_DIMENSION", 8):
                trainer._validate_image_file(image)
            with mock.patch.object(trainer, "MAX_IMAGE_PIXELS", 64):
                trainer._validate_image_file(image)
            with mock.patch.object(trainer, "MAX_IMAGE_PIXELS", 63):
                with self.assertRaisesRegex(contract.ContractError, "pixels"):
                    trainer._validate_image_file(image)
            payload[16:20] = (9).to_bytes(4, "big")
            image.write_bytes(payload)
            with mock.patch.object(trainer, "MAX_IMAGE_DIMENSION", 8):
                with self.assertRaisesRegex(contract.ContractError, "dimensions"):
                    trainer._validate_image_file(image)
            payload[16:20] = (1).to_bytes(4, "big")
            payload[25] = 6
            image.write_bytes(payload)
            trainer._validate_image_file(image)
            payload[25] = 7
            image.write_bytes(payload)
            with self.assertRaisesRegex(contract.ContractError, "channel count"):
                trainer._validate_image_file(image)

    def test_image_folder_rejects_extension_and_signature_mismatch(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "a").mkdir()
            (root / "b").mkdir()
            (root / "a/one.txt").write_bytes(PNG_BYTES)
            (root / "b/one.png").write_bytes(PNG_BYTES)
            (root / "b/two.png").write_bytes(PNG_BYTES)
            with self.assertRaisesRegex(contract.ContractError, "unsupported image extension"):
                trainer.inspect_image_folder(root)
            (root / "a/one.txt").unlink()
            (root / "a/one.png").write_bytes(b"not-an-image")
            (root / "a/two.png").write_bytes(PNG_BYTES)
            with self.assertRaisesRegex(contract.ContractError, "invalid PNG header"):
                trainer.inspect_image_folder(root)

    def test_image_contract_smoke_extracts_tar(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / "images.tar"
            with tarfile.open(archive, "w") as writer:
                for name in ("cat/one.png", "cat/two.png", "dog/one.png", "dog/two.png"):
                    member = tarfile.TarInfo(name)
                    member.size = len(PNG_BYTES)
                    writer.addfile(member, io.BytesIO(PNG_BYTES))
            output = root / "output"
            trainer.contract_smoke(image_config(archive), archive, output, root / "work")
            self.assertEqual((output / trainer.ARTIFACT_NAME).read_bytes()[4:8], b"TFL3")
            metrics = json.loads((output / trainer.METRICS_NAME).read_text(encoding="utf-8"))
            self.assertEqual(metrics["labels"], ["cat", "dog"])


if __name__ == "__main__":
    unittest.main()
