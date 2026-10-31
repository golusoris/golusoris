# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Positive, negative, and boundary tests using Go's real module parser."""

from __future__ import annotations

import os
import shutil
import tempfile
import unittest
import unittest.mock
from pathlib import Path

import gomod


class GoModTest(unittest.TestCase):
    """Exercise official parsing and fail-closed command handling."""

    def setUp(self) -> None:
        """Require the go binary and give each test a fresh go.mod path."""
        if shutil.which("go") is None:
            self.fail("go required for go.mod parser tests")
        directory = tempfile.TemporaryDirectory(prefix="golusoris-gomod-test-")
        self.addCleanup(directory.cleanup)
        self.path = Path(directory.name) / "go.mod"

    def _read(self, text: str) -> gomod.GoMod:
        """Write text as go.mod, parse it, and check the parser left the file unchanged."""
        self.path.write_text(text, encoding="utf-8")
        parsed = gomod.read_go_mod(self.path)
        self.assertEqual(self.path.read_text(encoding="utf-8"), text)
        return parsed

    def test_positive_require_and_replace_blocks_and_quoted_paths(self) -> None:
        """Single-line and block require/replace entries, quoted paths and comments parse."""
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
        """Syntax Go rejects raises GoModError instead of a partial result."""
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
        """An empty require block and a versioned replace yield only the replace target."""
        parsed = self._read(
            "module example.com/fixture\nrequire (\n)\n"
            "replace example.com/dep v1.2.0 => example.com/remote v1.3.0\n"
        )
        self.assertEqual(parsed, gomod.GoMod(frozenset(), ("example.com/remote",)))

    def test_boundary_no_dependencies(self) -> None:
        """Files without requires or replaces yield an empty GoMod."""
        for fixture in ("", "module example.com/fixture\n", "require (\n)\nreplace (\n)\n"):
            with self.subTest(fixture=fixture):
                self.assertEqual(self._read(fixture), gomod.GoMod(frozenset(), ()))

    def test_boundary_entry_limit_and_negative_overflow(self) -> None:
        """MAX_ENTRIES entries parse; one more raises GoModError."""
        fixture = "".join(
            f"require example.com/dep{index} v1.0.0\n"
            for index in range(gomod.MAX_ENTRIES)
        )
        self.assertEqual(len(self._read(fixture).requires), gomod.MAX_ENTRIES)
        with self.assertRaisesRegex(gomod.GoModError, "entry bound"):
            self._read(fixture + "replace example.com/dep0 => ./x\n")

    def test_negative_missing_go_binary(self) -> None:
        """A PATH without go raises GoModError."""
        with unittest.mock.patch.dict(os.environ, {"PATH": str(self.path.parent)}):
            with self.assertRaisesRegex(gomod.GoModError, "go.mod"):
                self._read("module example.com/fixture\n")

    def test_negative_missing_go_mod(self) -> None:
        """A missing go.mod raises GoModError."""
        with self.assertRaisesRegex(gomod.GoModError, "go.mod"):
            gomod.read_go_mod(self.path)

    def test_negative_not_a_go_mod_file(self) -> None:
        """A directory or a file with another name is refused before Go runs."""
        other = self.path.with_name("other.mod")
        other.write_text("module example.com/fixture\n", encoding="utf-8")
        for target in (self.path.parent, other):
            with self.subTest(target=target), unittest.mock.patch.object(
                gomod.subprocess, "run"
            ) as run, self.assertRaisesRegex(gomod.GoModError, "regular go.mod"):
                gomod.read_go_mod(target)
            run.assert_not_called()

    def test_negative_timeout(self) -> None:
        """A go command past its 30 s timeout raises GoModError."""
        self.path.write_text("module example.com/fixture\n", encoding="utf-8")
        with unittest.mock.patch.object(
            gomod.subprocess, "run", side_effect=gomod.subprocess.TimeoutExpired("go", 30)
        ) as run:
            with self.assertRaises(gomod.GoModError):
                gomod.read_go_mod(self.path)
        self.assertEqual(run.call_args.kwargs["timeout"], 30)

    def test_negative_bad_json(self) -> None:
        """JSON output of the wrong shape raises GoModError."""
        self.path.write_text("module example.com/fixture\n", encoding="utf-8")
        for output in ("not json", "[]", '{"Require": [1]}', '{"Replace": "bad"}'):
            result = gomod.subprocess.CompletedProcess(["go"], 0, stdout=output, stderr="")
            with self.subTest(output=output), unittest.mock.patch.object(
                gomod.subprocess, "run", return_value=result
            ), self.assertRaises(gomod.GoModError):
                gomod.read_go_mod(self.path)


if __name__ == "__main__":
    unittest.main()
