# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Positive, negative, and boundary tests for the shared policy-check git helpers."""

from __future__ import annotations

import os
import subprocess  # Runs only this repository's own policy script. # nosec B404
import sys
import tempfile
import unittest
import unittest.mock
from pathlib import Path

import repo_git


class RepoGitCase(unittest.TestCase):
    """Give each test a scratch directory with the caller's git variables removed."""

    def setUp(self) -> None:
        """Create the scratch directory inside an isolated git environment."""
        isolation = repo_git.scratch_environ()
        isolation.__enter__()
        self.addCleanup(isolation.__exit__, None, None, None)
        directory = tempfile.TemporaryDirectory(prefix="golusoris-repo-git-test-")
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)

    def write(self, rel: str, text: str = "fixture\n") -> Path:
        """Write text to rel under the scratch directory, creating parents."""
        path = self.root / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")
        return path

    def listed(self, name: str = "go.mod") -> list[str]:
        """Return worktree_files results relative to the scratch directory."""
        return [p.relative_to(self.root).as_posix() for p in repo_git.worktree_files(self.root, name)]


class WorktreeFilesTest(RepoGitCase):
    """Exercise the git-backed listing that replaced the filesystem walks."""

    def setUp(self) -> None:
        """Initialise a scratch repository that ignores ignored/."""
        super().setUp()
        repo_git.run_git(["init", "-q"], self.root)
        self.write(".gitignore", "/ignored/\n")

    def test_tracked_and_untracked_files_are_listed(self) -> None:
        """Positive: root, tracked nested, and untracked nested files are all listed."""
        self.write("go.mod")
        self.write("a/b/go.mod")
        repo_git.run_git(["add", "."], self.root)
        self.write("untracked/go.mod")
        self.assertEqual(self.listed(), ["a/b/go.mod", "go.mod", "untracked/go.mod"])

    def test_ignored_deleted_and_similar_names_are_not_listed(self) -> None:
        """Negative: ignored, deleted tracked, and differently named files stay out."""
        self.write("ignored/sub/go.mod")
        deleted = self.write("deleted/go.mod")
        self.write("x/xgo.mod")
        self.write("x/go.mod.bak")
        repo_git.run_git(["add", "."], self.root)
        deleted.unlink()
        self.assertEqual(self.listed(), [])

    def test_dangling_symlink_is_listed(self) -> None:
        """Boundary: a dangling symlink stays in the list so callers report it."""
        try:
            (self.root / "go.mod").symlink_to(self.root / "missing" / "go.mod")
        except OSError as error:
            self.skipTest(f"symlinks unsupported: {error}")
        self.assertEqual(self.listed(), ["go.mod"])

    def test_inherited_repository_overrides_are_ignored(self) -> None:
        """Boundary: inherited GIT_DIR and GIT_INDEX_FILE overrides do not redirect the listing."""
        self.write("go.mod")
        self.write("ignored/sub/go.mod")
        repo_git.run_git(["add", "."], self.root)
        decoy_dir = tempfile.TemporaryDirectory(prefix="golusoris-repo-git-decoy-")
        self.addCleanup(decoy_dir.cleanup)
        decoy = Path(decoy_dir.name)
        repo_git.run_git(["init", "-q"], decoy)
        decoy_sub = decoy / "ignored" / "sub" / "go.mod"
        decoy_sub.parent.mkdir(parents=True, exist_ok=True)
        decoy_sub.write_text("fixture\n", encoding="utf-8")
        repo_git.run_git(["add", "."], decoy)
        overrides = {
            "GIT_DIR": str(decoy / ".git"),
            "GIT_INDEX_FILE": str(decoy / ".git" / "index"),
        }
        for var, value in overrides.items():
            with self.subTest(var=var), unittest.mock.patch.dict(os.environ, {var: value}):
                self.assertEqual(self.listed(), ["go.mod"])

    def test_non_repository_fails_closed(self) -> None:
        """Negative: a directory outside any repository raises GitError."""
        outside = tempfile.TemporaryDirectory(prefix="golusoris-repo-git-outside-")
        self.addCleanup(outside.cleanup)
        with unittest.mock.patch.dict(os.environ, {"GIT_CEILING_DIRECTORIES": outside.name}):
            with self.assertRaises(repo_git.GitError):
                repo_git.worktree_files(Path(outside.name), "go.mod")


class ScratchEnvironTest(unittest.TestCase):
    """Exercise variable removal, restoration, and fail-closed lookup."""

    def setUp(self) -> None:
        """Give each test a scratch directory and a decoy index path inside it."""
        directory = tempfile.TemporaryDirectory(prefix="golusoris-repo-git-env-test-")
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        self.decoy = self.root / "decoy-index"

    def fixture(self) -> None:
        """Initialise a repository and stage one file, as the policy fixtures do."""
        repo_git.run_git(["init", "-q"], self.root)
        (self.root / "file.txt").write_text("fixture\n", encoding="utf-8")
        repo_git.run_git(["add", "."], self.root)

    def test_hook_index_does_not_receive_fixture_writes(self) -> None:
        """Positive: an inherited GIT_INDEX_FILE stays untouched by git add in the fixture."""
        with unittest.mock.patch.dict(os.environ, {"GIT_INDEX_FILE": str(self.decoy)}):
            with repo_git.scratch_environ():
                self.assertNotIn("GIT_INDEX_FILE", os.environ)
                self.fixture()
            self.assertEqual(os.environ["GIT_INDEX_FILE"], str(self.decoy))
        self.assertFalse(self.decoy.exists())
        self.assertTrue((self.root / ".git" / "index").is_file())

    def test_without_isolation_the_decoy_index_is_written(self) -> None:
        """Negative control: the same fixture without isolation writes the inherited index."""
        with unittest.mock.patch.dict(os.environ, {"GIT_INDEX_FILE": str(self.decoy)}):
            self.fixture()
        self.assertTrue(self.decoy.is_file())

    def test_unset_and_unrelated_variables_are_left_alone(self) -> None:
        """Boundary: unset local variables stay unset; non-local GIT_ variables are kept."""
        with unittest.mock.patch.dict(os.environ, {"GIT_AUTHOR_NAME": "fixture"}):
            os.environ.pop("GIT_DIR", None)
            with repo_git.scratch_environ():
                self.assertEqual(os.environ["GIT_AUTHOR_NAME"], "fixture")
            self.assertNotIn("GIT_DIR", os.environ)
            self.assertEqual(os.environ["GIT_AUTHOR_NAME"], "fixture")

    def test_missing_git_fails_closed(self) -> None:
        """Negative: without git on PATH the lookup raises and the environment is unchanged."""
        with unittest.mock.patch.dict(os.environ, {"PATH": "", "GIT_INDEX_FILE": str(self.decoy)}):
            with self.assertRaises(repo_git.GitError), repo_git.scratch_environ():
                self.fail("scratch_environ entered without git")
            self.assertEqual(os.environ["GIT_INDEX_FILE"], str(self.decoy))


class CallerExitCodeTest(unittest.TestCase):
    """Pin how a policy script reports a git failure to its caller."""

    def test_renovate_policy_exits_3_without_git(self) -> None:
        """Negative: missing git makes renovate-policy-test.py exit 3, not 1 with a traceback."""
        script = Path(__file__).with_name("renovate-policy-test.py")
        proc = subprocess.run(  # noqa: S603 - runs this repository's own script. # nosec B603
            [sys.executable, "-B", str(script)],
            env={**os.environ, "PATH": ""},
            capture_output=True,
            text=True,
            timeout=120,
            check=False,
        )
        self.assertEqual(proc.returncode, 3, proc.stderr)
        self.assertIn("ERROR: git", proc.stderr)
        self.assertNotIn("Traceback", proc.stderr)


if __name__ == "__main__":
    unittest.main()
