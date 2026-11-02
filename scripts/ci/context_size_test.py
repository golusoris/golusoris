#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Tests for context_size.py: the repository passes, planted oversized files fail."""

from __future__ import annotations

import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import context_size

ROOT = Path(__file__).resolve().parents[2]
CAP = context_size.WINDSURF_CHARS_PER_FILE


class ContextSizeTest(unittest.TestCase):
    """Positive, negative and boundary cases for both client caps."""

    def setUp(self) -> None:
        scratch = tempfile.TemporaryDirectory()
        self.addCleanup(scratch.cleanup)
        self.root = Path(scratch.name)
        self.git("init", "-q")

    def git(self, *args: str) -> None:
        """Run one git command in the scratch repository."""
        git = shutil.which("git")
        self.assertIsNotNone(git)
        subprocess.run(  # noqa: S603 - fixed git command on runner PATH.
            [str(git), *args], cwd=self.root, check=True, capture_output=True, timeout=60
        )

    def write(self, name: str, text: str, *, stage: bool = True) -> None:
        """Write a file into the scratch repository and optionally stage it."""
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(text.encode("utf-8"))
        if stage:
            self.git("add", "--", name)

    def check(self) -> list[str]:
        """Return the checker's findings for the scratch repository."""
        return context_size.oversized(context_size.agents_contents(self.root))

    def test_repository_agents_files_fit_every_cap(self) -> None:
        contents = context_size.agents_contents(ROOT)
        self.assertIn("AGENTS.md", contents)
        self.assertEqual(context_size.oversized(contents), [])

    def test_file_at_windsurf_cap_passes(self) -> None:
        self.write("AGENTS.md", "ä" * CAP)
        self.assertEqual(self.check(), [])

    def test_file_past_windsurf_cap_fails(self) -> None:
        self.write("AGENTS.md", "a" * (CAP + 1))
        found = self.check()
        self.assertEqual(len(found), 1)
        self.assertIn("Windsurf cap", found[0])

    def test_staged_content_decides_not_the_working_tree(self) -> None:
        self.write("AGENTS.md", "a" * (CAP + 1))
        (self.root / "AGENTS.md").write_text("short", encoding="utf-8")
        self.assertEqual(len(self.check()), 1)

    def test_deleted_working_tree_file_is_still_checked(self) -> None:
        self.write("AGENTS.md", "a" * (CAP + 1))
        (self.root / "AGENTS.md").unlink()
        self.assertEqual(len(self.check()), 1)

    def test_untracked_file_is_checked(self) -> None:
        self.write("new/AGENTS.md", "a" * (CAP + 1), stage=False)
        self.assertIn("new/AGENTS.md", self.check()[0])

    def test_path_with_space_is_checked(self) -> None:
        self.write("user guide/AGENTS.md", "a" * (CAP + 1))
        self.assertIn("user guide/AGENTS.md", self.check()[0])

    def test_other_names_ending_in_agents_md_are_ignored(self) -> None:
        self.write("ARCHIVE-AGENTS.md", "a" * (CAP + 1))
        self.write("loose-AGENTS.md", "a" * (CAP + 1), stage=False)
        self.assertEqual(self.check(), [])

    def test_crlf_endings_count_as_lf(self) -> None:
        self.write("AGENTS.md", "a\r\n" * 4001, stage=False)
        self.assertEqual(self.check(), [])

    def test_chain_past_codex_cap_fails_only_on_the_deep_file(self) -> None:
        for name in ("AGENTS.md", "a/AGENTS.md", "a/b/AGENTS.md"):
            self.write(name, "a" * 11_000)
        found = self.check()
        self.assertEqual(len(found), 1)
        self.assertIn("a/b/AGENTS.md: chain 33000 B > Codex cap", found[0])

    def test_sibling_files_do_not_share_a_chain(self) -> None:
        for name in ("AGENTS.md", "a/AGENTS.md", "b/AGENTS.md"):
            self.write(name, "a" * 11_000)
        self.assertEqual(self.check(), [])


if __name__ == "__main__":
    unittest.main()
