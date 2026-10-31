#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Unit tests for ci_scope.py."""

from __future__ import annotations

import os
import re
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

import ci_scope

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

    def test_negative_composite_action_change_forces_full_mode(self) -> None:
        """Shared action changes must select every module's gates."""
        action = self.repo / ".github/actions/setup-go/action.yml"
        action.parent.mkdir(parents=True)
        action.write_text("name: changed action\n", encoding="utf-8")
        self._git("add", ".")
        self._git("commit", "-m", "change shared action")

        proc = self._run_scope("modules", base=self.base_sha)
        self.assertEqual(proc.returncode, 0)
        self.assertIn("global trigger: .github/actions/setup-go/action.yml", proc.stderr)
        self.assertIn("k8s/nri", proc.stdout.splitlines())

    def test_negative_ruff_config_change_lints_every_python_file(self) -> None:
        """A ruff.toml-only change selects every Python file, not none."""
        (self.repo / "tool.py").write_text("value = 1\n", encoding="utf-8")
        self._git("add", ".")
        self._git("commit", "-m", "add python")
        base = self._git("rev-parse", "HEAD").strip()
        (self.repo / "ruff.toml").write_text('[lint]\nselect = ["S"]\n', encoding="utf-8")
        self._git("add", ".")
        self._git("commit", "-m", "change ruff rules")

        proc = self._run_scope("files", "*.py", base=base)
        self.assertEqual(proc.returncode, 0)
        self.assertIn("global trigger: ruff.toml", proc.stderr)
        self.assertIn("tool.py", proc.stdout.splitlines())

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


    def _write(self, rel: str, text: str = "x\n") -> None:
        path = self.repo / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")

    def _gate(self) -> tuple[str, str]:
        proc = self._run_scope("gate", base=self.base_sha)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        return proc.stdout.strip(), proc.stderr

    def test_positive_gate_docs_only_skips(self) -> None:
        """A diff of only skip-safe paths skips the gated jobs."""
        for rel in (
            "docs/guide/page.md",
            "README.md",
            "core/AGENTS.md",
            "LICENSES/MIT.txt",
            ".claude/settings.json",
            ".github/ISSUE_TEMPLATE/bug.yml",
            "mkdocs.yml",
        ):
            self._write(rel)
        self._git("add", ".")
        self._git("commit", "-m", "docs only")
        decision, stderr = self._gate()
        self.assertEqual(decision, "skip")
        self.assertIn("ci-scope: gate=skip", stderr)

    def test_negative_gate_unaffected_by_malformed_go_mod(self) -> None:
        """The gate decides from paths without invoking Go or parsing modules."""
        self._write("go.mod", "module example.com/root\nreplace example.com/core =>\n")
        self._git("commit", "-am", "malformed module fixture")
        self.base_sha = self._git("rev-parse", "HEAD").strip()
        self._write("docs/guide.md")
        self._git("add", ".")
        self._git("commit", "-m", "docs")
        with mock.patch.dict(os.environ, {"CI_SCOPE_BASE": self.base_sha}), mock.patch.object(
            ci_scope.gomod, "read_go_mod", side_effect=AssertionError("gate must not invoke Go")
        ):
            decision, reason = ci_scope.evaluate_gate(self.repo)
        self.assertEqual(decision, "skip")
        self.assertIn("skip-safe", reason)

    def test_negative_malformed_go_mod_selects_all_modules(self) -> None:
        """A broken replace cannot silently remove reverse dependency edges."""
        self._write("go.mod", "module example.com/root\nreplace example.com/core =>\n")
        self._git("commit", "-am", "malformed module fixture")
        self.base_sha = self._git("rev-parse", "HEAD").strip()
        self._write("core/core.go", "package core\n// change\n")
        self._git("commit", "-am", "core change")
        proc = self._run_scope("modules", base=self.base_sha)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(
            proc.stdout.splitlines(),
            [".", "container/registry", "container/registry/sign", "core", "k8s/nri"],
        )
        self.assertIn("ci-scope: full", proc.stderr)
        self.assertIn("go.mod", proc.stderr)

    def test_negative_gate_go_file_runs(self) -> None:
        """One Go file next to docs runs the gated jobs."""
        self._write("docs/guide.md")
        self._write("core/core.go", "package core\n// change\n")
        self._git("add", ".")
        self._git("commit", "-m", "docs and code")
        decision, stderr = self._gate()
        self.assertEqual(decision, "run")
        self.assertIn("gated input changed: core/core.go", stderr)

    def test_negative_gate_unknown_path_runs(self) -> None:
        """A path outside the allow-list runs the gated jobs."""
        self._write("unknown.txt")
        self._git("add", ".")
        self._git("commit", "-m", "unknown")
        self.assertEqual(self._gate()[0], "run")

    def test_negative_gate_global_trigger_runs(self) -> None:
        """A global trigger forces a full run."""
        self._write("scripts/ci/check.sh", "#!/bin/sh\n")
        self._git("add", ".")
        self._git("commit", "-m", "ci script")
        self.assertEqual(self._gate()[0], "run")

    def test_negative_gate_rename_into_docs_runs(self) -> None:
        """Moving code under docs/ still counts the removed source path."""
        (self.repo / "docs").mkdir()
        self._git("mv", "core/core.go", "docs/core.go.txt")
        self._git("commit", "-m", "move")
        decision, stderr = self._gate()
        self.assertEqual(decision, "run")
        self.assertIn("core/core.go", stderr)

    def test_boundary_gate_empty_diff_runs(self) -> None:
        """An empty diff fails closed."""
        decision, stderr = self._gate()
        self.assertEqual(decision, "run")
        self.assertIn("no changed files", stderr)

    def test_boundary_gate_full_mode_runs(self) -> None:
        """Without a base the gate always runs."""
        self._write("docs/guide.md")
        self._git("add", ".")
        self._git("commit", "-m", "docs")
        proc = self._run_scope("gate", base=None)
        self.assertEqual(proc.returncode, 0)
        self.assertEqual(proc.stdout.strip(), "run")

    def test_negative_gate_bad_base_is_error(self) -> None:
        """A git error exits 3 and prints no decision."""
        proc = self._run_scope("gate", base="deadbeefbadsha00000000000000000000000000")
        self.assertEqual(proc.returncode, 3)
        self.assertEqual(proc.stdout, "")


class SkipSafeEmbedTest(unittest.TestCase):
    """No file a Go package embeds may be skip-safe."""

    def test_no_embedded_file_is_skip_safe(self) -> None:
        sys.path.insert(0, str(CI_SCOPE_SCRIPT.parent))
        import ci_scope

        directive = re.compile(r"^//go:embed\s+(.+)$", re.MULTILINE)
        listed = subprocess.run(  # noqa: S603 - fixed git command in this repository.
            ["git", "-C", str(REPO_ROOT), "ls-files", "-z", "--", "*.go"],  # noqa: S607 - git on PATH.
            capture_output=True,
            check=True,
            timeout=60,
        ).stdout.decode()
        embedded: list[str] = []
        for rel in filter(None, listed.split("\0")):
            if rel.endswith("_test.go"):
                continue
            text = (REPO_ROOT / rel).read_text(encoding="utf-8", errors="replace")
            for match in directive.finditer(text):
                for pattern in match.group(1).split():
                    target = REPO_ROOT / Path(rel).parent / pattern
                    files = [p for p in target.rglob("*") if p.is_file()] if target.is_dir() else [target]
                    embedded += [p.relative_to(REPO_ROOT).as_posix() for p in files]
        self.assertTrue(embedded, "expected //go:embed directives")
        clashes = [f for f in embedded if ci_scope._matches_any_pattern(f, ci_scope.SKIP_SAFE)]
        self.assertEqual(clashes, [])


if __name__ == "__main__":
    unittest.main()
