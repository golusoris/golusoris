# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Positive, negative, and boundary tests using Go's real module parser."""

from __future__ import annotations

import os
import shutil
import subprocess
import tempfile
import unittest
import unittest.mock
from pathlib import Path

import gomod


class GoModTest(unittest.TestCase):
    """Exercise official parsing and fail-closed command handling."""

    def setUp(self) -> None:
        if shutil.which("go") is None:
            self.fail("go required for go.mod parser tests")
        directory = tempfile.TemporaryDirectory(prefix="golusoris-gomod-test-")
        self.addCleanup(directory.cleanup)
        self.path = Path(directory.name) / "go.mod"

    def _read(self, text: str) -> gomod.GoMod:
        self.path.write_text(text, encoding="utf-8")
        parsed = gomod.read_go_mod(self.path)
        self.assertEqual(self.path.read_text(encoding="utf-8"), text)
        return parsed

    def test_positive_require_and_replace_blocks_and_quoted_paths(self) -> None:
        parsed = self._read(
            "module example.com/fixture\n"
            "require example.com/single v1.0.0 // indirect\n"
            "require (\n"
            '    "example.com/quoted" v1.2.0 // inline comment\n'
            "    example.com/block/v2 v2.0.0\n"
            ")\n"
            "replace example.com/single => ./x\n"
            "replace (\n"
            '    "example.com/quoted" => "../quoted path//child"\n'
            "    example.com/block/v2 => example.com/remote/v2 v2.3.0\n"
            ")\n"
        )
        self.assertEqual(
            parsed.requires,
            frozenset({"example.com/single", "example.com/quoted", "example.com/block/v2"}),
        )
        self.assertEqual(
            parsed.replace_targets,
            ("./x", "../quoted path//child", "example.com/remote/v2"),
        )

    def test_negative_malformed_go_mod(self) -> None:
        for fixture in (
            "require (\nexample.com/dep v1.0.0\n",
            "replace example.com/dep =>\n",
            "require example.com/dep\n",
            'require "example.com/unterminated v1.0.0\n',
            "unknown ignored\n",
        ):
            with self.subTest(fixture=fixture), self.assertRaises(gomod.GoModError):
                self._read(fixture)

    def test_boundary_empty_require_and_versioned_replace(self) -> None:
        parsed = self._read(
            "module example.com/fixture\nrequire (\n)\n"
            "replace example.com/dep v1.2.0 => example.com/remote v1.3.0\n"
        )
        self.assertEqual(parsed, gomod.GoMod(frozenset(), ("example.com/remote",)))

    def test_boundary_no_dependencies(self) -> None:
        for fixture in ("", "module example.com/fixture\n", "require (\n)\nreplace (\n)\n"):
            with self.subTest(fixture=fixture):
                self.assertEqual(self._read(fixture), gomod.GoMod(frozenset(), ()))

    def test_boundary_entry_limit_and_negative_overflow(self) -> None:
        fixture = "".join(
            f"require example.com/dep{index} v1.0.0\n"
            for index in range(gomod.MAX_ENTRIES)
        )
        self.assertEqual(len(self._read(fixture).requires), gomod.MAX_ENTRIES)
        with self.assertRaisesRegex(gomod.GoModError, "entry bound"):
            self._read(fixture + "replace example.com/dep0 => ./x\n")

    def test_negative_missing_go_binary(self) -> None:
        with unittest.mock.patch.dict(os.environ, {"PATH": str(self.path.parent)}):
            with self.assertRaisesRegex(gomod.GoModError, "go.mod"):
                self._read("module example.com/fixture\n")

    def test_negative_missing_go_mod(self) -> None:
        with self.assertRaisesRegex(gomod.GoModError, "go.mod"):
            gomod.read_go_mod(self.path)

    def test_negative_timeout(self) -> None:
        with unittest.mock.patch.object(
            gomod.subprocess, "run", side_effect=subprocess.TimeoutExpired("go", 30)
        ) as run:
            with self.assertRaises(gomod.GoModError):
                gomod.read_go_mod(self.path)
        self.assertEqual(run.call_args.kwargs["timeout"], 30)

    def test_negative_bad_json(self) -> None:
        for output in ("not json", "[]", '{"Require": [1]}', '{"Replace": "bad"}'):
            result = subprocess.CompletedProcess(["go"], 0, stdout=output, stderr="")
            with self.subTest(output=output), unittest.mock.patch.object(
                gomod.subprocess, "run", return_value=result
            ), self.assertRaises(gomod.GoModError):
                gomod.read_go_mod(self.path)


if __name__ == "__main__":
    unittest.main()
