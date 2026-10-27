#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Unit tests for ci_scope.py."""

from __future__ import annotations

import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
CI_SCOPE_SCRIPT = REPO_ROOT / "scripts" / "ci" / "ci_scope.py"


class CiScopeTest(unittest.TestCase):
    """Test suite covering positive, negative, and boundary cases for ci_scope.py."""

    def setUp(self) -> None:
        self.test_dir = tempfile.mkdtemp(prefix="golusoris-ci-scope-test-")
        self.repo = Path(self.test_dir)

        # Initialize test git repo
        self._git("init")
        self._git("config", "user.name", "Test Runner")
        self._git("config", "user.email", "test@example.com")
        self._git("config", "commit.gpgsign", "false")

        # Create module layout
        (self.repo / "core").mkdir(parents=True)
        (self.repo / "k8s" / "nri").mkdir(parents=True)
        (self.repo / "container" / "registry" / "sign").mkdir(parents=True)

        (self.repo / "go.mod").write_text(
            "module example.com/root\n\ngo 1.22\n\nreplace example.com/core => ./core\n",
            encoding="utf-8",
        )
        (self.repo / "core" / "go.mod").write_text(
            "module example.com/core\n\ngo 1.22\n",
            encoding="utf-8",
        )
        (self.repo / "k8s" / "nri" / "go.mod").write_text(
            "module example.com/k8s/nri\n\ngo 1.22\n",
            encoding="utf-8",
        )
        (self.repo / "container" / "registry" / "go.mod").write_text(
            "module example.com/container/registry\n\ngo 1.22\n\nreplace example.com/core => ../../core\n",
            encoding="utf-8",
        )
        (self.repo / "container" / "registry" / "sign" / "go.mod").write_text(
            "module example.com/container/registry/sign\n\ngo 1.22\n\n"
            "replace example.com/container/registry => ../\n",
            encoding="utf-8",
        )

        (self.repo / "main.go").write_text("package main\nfunc main() {}\n", encoding="utf-8")
        (self.repo / "core" / "core.go").write_text("package core\n", encoding="utf-8")
        (self.repo / "k8s" / "nri" / "nri.go").write_text("package nri\n", encoding="utf-8")
        (self.repo / "container" / "registry" / "reg.go").write_text("package registry\n", encoding="utf-8")
        (self.repo / "container" / "registry" / "sign" / "sign.go").write_text("package sign\n", encoding="utf-8")

        self._git("add", ".")
        self._git("commit", "-m", "initial commit")
        self.base_sha = self._git("rev-parse", "HEAD").strip()

    def tearDown(self) -> None:
        shutil.rmtree(self.test_dir, ignore_errors=True)

    def _git(self, *args: str) -> str:
        proc = subprocess.run(  # noqa: S603 - fixed git command in test repository.
            ["git", "-C", str(self.repo), *args],  # noqa: S607 - git executable on runner PATH.
            capture_output=True,
            text=True,
            check=True,
        )
        return proc.stdout

    def _run_scope(
        self,
        *args: str,
        base: str | None = None,
    ) -> subprocess.CompletedProcess[str]:
        env = os.environ.copy()
        if base is not None:
            env["CI_SCOPE_BASE"] = base
        else:
            env.pop("CI_SCOPE_BASE", None)

        return subprocess.run(  # noqa: S603 - fixed interpreter and repository script.
            [sys.executable, str(CI_SCOPE_SCRIPT), *args],
            cwd=self.repo,
            env=env,
            capture_output=True,
            text=True,
            check=False,
        )

    def test_positive_one_file_in_nested_module_selects_that_module_only(self) -> None:
        """Touching one file in nested module selects only that module."""
        (self.repo / "k8s" / "nri" / "new.go").write_text("package nri\n", encoding="utf-8")
        self._git("add", ".")
        self._git("commit", "-m", "touch k8s/nri")

        proc = self._run_scope("modules", base=self.base_sha)
        self.assertEqual(proc.returncode, 0)
        self.assertIn("ci-scope: diff-aware, 1 changed files vs", proc.stderr)
        lines = [line.strip() for line in proc.stdout.splitlines() if line.strip()]
        self.assertEqual(lines, ["k8s/nri"])

    def test_positive_core_file_selects_dependents(self) -> None:
        """Touching a core file selects core plus all its transitive dependents."""
        (self.repo / "core" / "core.go").write_text("package core\n// modified\n", encoding="utf-8")
        self._git("commit", "-am", "touch core")

        proc = self._run_scope("modules", base=self.base_sha)
        self.assertEqual(proc.returncode, 0)
        self.assertIn("ci-scope: diff-aware, 1 changed files vs", proc.stderr)
        lines = [line.strip() for line in proc.stdout.splitlines() if line.strip()]

        # core changed -> selects core
        self.assertIn("core", lines)
        # root replaces core -> selects .
        self.assertIn(".", lines)
        # container/registry replaces core -> selects container/registry
        self.assertIn("container/registry", lines)
        # container/registry/sign replaces container/registry -> selects sign
        self.assertIn("container/registry/sign", lines)
        # k8s/nri has no replace for core in this fixture -> not selected
        self.assertNotIn("k8s/nri", lines)

    def test_positive_files_subcommand(self) -> None:
        """Files subcommand returns changed files matching glob."""
        (self.repo / "k8s" / "nri" / "file.py").write_text("# py\n", encoding="utf-8")
        (self.repo / "k8s" / "nri" / "file.go").write_text("package nri\n", encoding="utf-8")
        self._git("add", ".")
        self._git("commit", "-m", "add py and go")

        proc = self._run_scope("files", "*.py", base=self.base_sha)
        self.assertEqual(proc.returncode, 0)
        lines = [line.strip() for line in proc.stdout.splitlines() if line.strip()]
        self.assertEqual(lines, ["k8s/nri/file.py"])

    def test_positive_any_subcommand(self) -> None:
        """Any subcommand returns 0 on match and 1 on mismatch."""
        (self.repo / "k8s" / "nri" / "file.py").write_text("# py\n", encoding="utf-8")
        self._git("add", ".")
        self._git("commit", "-m", "add py")

        match_proc = self._run_scope("any", "*.py", base=self.base_sha)
        self.assertEqual(match_proc.returncode, 0)
        self.assertEqual(match_proc.stdout, "")

        mismatch_proc = self._run_scope("any", "*.c", base=self.base_sha)
        self.assertEqual(mismatch_proc.returncode, 1)
        self.assertEqual(mismatch_proc.stdout, "")

    def test_negative_bad_sha_exits_nonzero(self) -> None:
        """Invalid base commit SHA fails closed with non-zero exit."""
        proc = self._run_scope("modules", base="deadbeefbadsha00000000000000000000000000")
        self.assertEqual(proc.returncode, 3)

    def test_negative_bad_sha_any_is_error_not_no_match(self) -> None:
        """A git error under `any` exits 3, never the no-match code 1."""
        proc = self._run_scope("any", "*.go", base="deadbeefbadsha00000000000000000000000000")
        self.assertEqual(proc.returncode, 3)
        self.assertIn("ci-scope: git error", proc.stderr)

    def test_negative_global_trigger_forces_full_mode(self) -> None:
        """Changing a global trigger file forces full mode even in diff mode."""
        (self.repo / "Makefile").write_text("all:\n", encoding="utf-8")
        self._git("add", ".")
        self._git("commit", "-m", "add Makefile")

        proc = self._run_scope("modules", base=self.base_sha)
        self.assertEqual(proc.returncode, 0)
        self.assertIn("ci-scope: full (global trigger: Makefile)", proc.stderr)
        lines = [line.strip() for line in proc.stdout.splitlines() if line.strip()]
        # Full mode selects all modules
        self.assertIn(".", lines)
        self.assertIn("core", lines)
        self.assertIn("k8s/nri", lines)
        self.assertIn("container/registry", lines)
        self.assertIn("container/registry/sign", lines)

    def test_boundary_empty_diff_returns_empty_and_count_zero(self) -> None:
        """Zero changed files yields count 0 and empty module list."""
        proc = self._run_scope("modules", base=self.base_sha)
        self.assertEqual(proc.returncode, 0)
        self.assertIn("ci-scope: diff-aware, 0 changed files vs", proc.stderr)
        lines = [line.strip() for line in proc.stdout.splitlines() if line.strip()]
        self.assertEqual(lines, [])

    def test_boundary_root_file_selects_root(self) -> None:
        """Changing a file in root module directory selects '.'."""
        (self.repo / "main.go").write_text("package main\n// update\n", encoding="utf-8")
        self._git("commit", "-am", "update main.go")

        proc = self._run_scope("modules", base=self.base_sha)
        self.assertEqual(proc.returncode, 0)
        self.assertIn("ci-scope: diff-aware, 1 changed files vs", proc.stderr)
        lines = [line.strip() for line in proc.stdout.splitlines() if line.strip()]
        self.assertIn(".", lines)
        self.assertNotIn("k8s/nri", lines)


if __name__ == "__main__":
    unittest.main()
