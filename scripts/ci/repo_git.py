# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Git helpers for scripts/ci policy checks: worktree file listing and fixture isolation."""

from __future__ import annotations

import contextlib
import os
import subprocess  # Runs only git with arguments fixed by the calling script. # nosec B404
import tempfile
from collections.abc import Generator
from pathlib import Path

GIT_TIMEOUT_SECONDS = 30


class GitError(Exception):
    """Raised when git cannot run, times out, or exits non-zero."""


def run_git(
    args: list[str],
    cwd: Path | None = None,
    timeout: int = GIT_TIMEOUT_SECONDS,
    *,
    env: dict[str, str] | None = None,
) -> str:
    """Run one git command with a timeout and return its stdout."""
    try:
        proc = subprocess.run(  # noqa: S603 - arguments are fixed by the calling script. # nosec B603
            ["git", *args],  # noqa: S607 - git resolves from the runner PATH.
            cwd=cwd,
            capture_output=True,
            text=True,
            timeout=timeout,
            check=False,
            env=env,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        raise GitError(f"git {' '.join(args)} failed to run: {error}") from error
    if proc.returncode != 0:
        raise GitError(f"git {' '.join(args)} exited {proc.returncode}: {proc.stderr.strip()}")
    return proc.stdout


def local_env_names(timeout: int = GIT_TIMEOUT_SECONDS) -> set[str]:
    """Return environment variable names local to a git repository."""
    return set(run_git(["rev-parse", "--local-env-vars"], timeout=timeout).split())


def fixture_environ() -> dict[str, str]:
    """Isolate fixture git commands from hook variables and user configuration."""
    local_vars = local_env_names()
    env = {
        key: value for key, value in os.environ.items()
        if key not in local_vars and not key.startswith("GIT_")
    }
    env.update(GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_NOSYSTEM="1")
    return env


@contextlib.contextmanager
def decoy_environ(timeout: int = GIT_TIMEOUT_SECONDS) -> Generator[None, None, None]:
    """Point hook git variables at a decoy repository; fail if fixture git touched its index."""
    with tempfile.TemporaryDirectory() as directory:
        decoy = Path(directory)
        env = fixture_environ()
        run_git(["init", "-q"], decoy, timeout, env=env)
        (decoy / "sentinel").write_text("decoy index must survive", encoding="utf-8")
        run_git(["add", "sentinel"], decoy, timeout, env=env)
        index = decoy / ".git" / "index"
        before = index.read_bytes()
        shallow = decoy / "invalid-shallow"
        shallow.write_text("invalid object id\n", encoding="utf-8")
        hostile = {
            "GIT_DIR": str(decoy / ".git"),
            "GIT_INDEX_FILE": str(index),
            "GIT_SHALLOW_FILE": str(shallow),
        }
        saved = {name: os.environ.get(name) for name in hostile}
        os.environ.update(hostile)
        try:
            yield
        finally:
            for name, value in saved.items():
                if value is None:
                    os.environ.pop(name, None)
                else:
                    os.environ[name] = value
        if index.read_bytes() != before:
            raise GitError("decoy index changed: a fixture git command followed hook variables")

def worktree_files(root: Path, name: str, timeout: int = GIT_TIMEOUT_SECONDS) -> list[Path]:
    """List tracked and untracked files called name under root that git does not ignore."""
    # Local git variables must not redirect the index or working tree.
    local_vars = local_env_names(timeout=timeout)
    env = {key: value for key, value in os.environ.items() if key not in local_vars}
    # git skips ignored paths, so linked worktrees under .claude/worktrees/ stay out.
    stdout = run_git(
        ["ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", name, f"**/{name}"],
        root,
        timeout,
        env=env,
    )
    # --cached also lists deleted tracked files; dangling symlinks stay so callers report them.
    found = {root / token for token in stdout.split("\0") if token}
    return sorted(path for path in found if path.is_file() or path.is_symlink())


@contextlib.contextmanager
def scratch_environ(timeout: int = GIT_TIMEOUT_SECONDS) -> Generator[None, None, None]:
    """Remove repository-local git variables from os.environ and restore them on exit."""
    # A git hook exports these; inherited, they point fixture commands at the caller's index.
    names = local_env_names(timeout=timeout)
    saved = {name: os.environ.pop(name) for name in names if name in os.environ}
    try:
        yield
    finally:
        os.environ.update(saved)
