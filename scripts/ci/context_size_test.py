#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Tests for context_size.py: the repository passes, planted oversized files fail."""

from __future__ import annotations

import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import context_size

ROOT = Path(__file__).resolve().parents[2]


class ContextSizeTest(unittest.TestCase):
    """Positive, negative and boundary cases for both client caps."""

    def check(self, files: dict[str, str]) -> list[str]:
        """Write files into a scratch root and return the checker's findings."""
        with tempfile.TemporaryDirectory() as scratch:
            root = Path(scratch)
            for name, text in files.items():
                (root / name).parent.mkdir(parents=True, exist_ok=True)
                (root / name).write_text(text, encoding="utf-8")
            return context_size.oversized(root, sorted(files))

    def test_repository_agents_files_fit_every_cap(self) -> None:
        files = context_size.tracked_agents_files(ROOT)
        self.assertIn("AGENTS.md", files)
        self.assertEqual(context_size.oversized(ROOT, files), [])

    def test_file_at_windsurf_cap_passes(self) -> None:
        self.assertEqual(self.check({"AGENTS.md": "ä" * context_size.WINDSURF_CHARS_PER_FILE}), [])

    def test_file_past_windsurf_cap_fails(self) -> None:
        found = self.check({"AGENTS.md": "a" * (context_size.WINDSURF_CHARS_PER_FILE + 1)})
        self.assertEqual(len(found), 1)
        self.assertIn("Windsurf cap", found[0])

    def test_chain_past_codex_cap_fails_only_on_the_deep_file(self) -> None:
        part = "a" * 11_000
        found = self.check({"AGENTS.md": part, "a/AGENTS.md": part, "a/b/AGENTS.md": part})
        self.assertEqual(len(found), 1)
        self.assertIn("a/b/AGENTS.md: chain 33000 B > Codex cap", found[0])

    def test_sibling_files_do_not_share_a_chain(self) -> None:
        part = "a" * 11_000
        found = self.check({"AGENTS.md": part, "a/AGENTS.md": part, "b/AGENTS.md": part})
        self.assertEqual(found, [])


if __name__ == "__main__":
    unittest.main()
