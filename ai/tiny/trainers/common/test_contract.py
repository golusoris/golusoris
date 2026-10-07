# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

from __future__ import annotations

import io
import json
import tarfile
import tempfile
import unittest
import zipfile
from pathlib import Path

from trainers.common import contract


class ContractTest(unittest.TestCase):
    def test_load_and_materialize_staged_file_at_exact_limit(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            dataset = root / "dataset"
            dataset.write_bytes(b"1234")
            config_path = root / "config.json"
            config_path.write_text(
                json.dumps(
                    {
                        "dataset_uri": dataset.as_uri(),
                        "max_dataset_bytes": 4,
                    }
                ),
                encoding="utf-8",
            )
            config_value = contract.load_config(config_path)
            actual = contract.materialize_dataset(config_value, root / "work", root)
            self.assertEqual(actual, dataset)

    def test_materialize_rejects_file_outside_input_root(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            input_root = root / "input"
            input_root.mkdir()
            outside = root / "outside"
            outside.write_text("no", encoding="utf-8")
            with self.assertRaisesRegex(contract.ContractError, "remain under"):
                contract.materialize_dataset(
                    {"dataset_uri": outside.as_uri(), "max_dataset_bytes": 2},
                    root / "work",
                    input_root,
                )

    def test_materialize_rejects_one_byte_over_limit(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            dataset = root / "dataset"
            dataset.write_bytes(b"12345")
            with self.assertRaisesRegex(contract.ContractError, "exceeds 4"):
                contract.materialize_dataset(
                    {"dataset_uri": dataset.as_uri(), "max_dataset_bytes": 4},
                    root / "work",
                    root,
                )

    def test_materialize_rejects_remote_dataset_schemes(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            for uri in ("https://example.com/data.csv", "s3://tenant-bucket/data.csv"):
                with self.subTest(uri=uri), self.assertRaisesRegex(
                    contract.ContractError, "stage remote data"
                ):
                    contract.materialize_dataset(
                        {"dataset_uri": uri, "max_dataset_bytes": 4},
                        root / "work",
                        root,
                    )

    def test_materialize_rejects_symlink_inside_input_root(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            target = root / "target"
            target.write_bytes(b"data")
            link = root / "dataset"
            try:
                link.symlink_to(target)
            except OSError as exc:
                self.skipTest(f"symlinks unavailable: {exc}")
            with self.assertRaisesRegex(contract.ContractError, "symbolic links"):
                contract.materialize_dataset(
                    {"dataset_uri": link.as_uri(), "max_dataset_bytes": 4},
                    root / "work",
                    root,
                )

    def test_zip_extracts_regular_files(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / "images.zip"
            with zipfile.ZipFile(archive, "w") as writer:
                writer.writestr("cat/one.jpg", b"image")
            destination = root / "images"
            contract.safe_extract_archive(archive, destination, "zip", 5)
            self.assertEqual((destination / "cat/one.jpg").read_bytes(), b"image")

    def test_zip_rejects_path_traversal(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / "bad.zip"
            with zipfile.ZipFile(archive, "w") as writer:
                writer.writestr("../escape", b"bad")
            with self.assertRaisesRegex(contract.ContractError, "escapes"):
                contract.safe_extract_archive(archive, root / "images", "zip", 100)

    def test_tar_rejects_symbolic_link(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / "bad.tar"
            with tarfile.open(archive, "w") as writer:
                member = tarfile.TarInfo("link")
                member.type = tarfile.SYMTYPE
                member.linkname = "/etc/passwd"
                writer.addfile(member)
            with self.assertRaisesRegex(contract.ContractError, "special files"):
                contract.safe_extract_archive(archive, root / "images", "tar", 100)

    def test_bounded_copy_accepts_exact_limit_and_rejects_next_byte(self) -> None:
        destination = io.BytesIO()
        self.assertEqual(contract._copy_bounded(io.BytesIO(b"1234"), destination, 4), 4)
        with self.assertRaisesRegex(contract.ContractError, "beyond 4"):
            contract._copy_bounded(io.BytesIO(b"12345"), io.BytesIO(), 4)


if __name__ == "__main__":
    unittest.main()
