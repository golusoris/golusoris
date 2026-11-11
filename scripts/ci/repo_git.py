# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Git helpers for scripts/ci policy checks: worktree file listing and fixture isolation."""

from __future__ import annotations

import contextlib
import os
import subprocess  # Runs only git with arguments fixed by the calling script. # nosec B404
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
        raise GitError(
            f"git {' '.join(args)} exited {proc.returncode}: {proc.stderr.strip()}"
        )
    return proc.stdout


def local_env_names(timeout: int = GIT_TIMEOUT_SECONDS) -> set[str]:
    """Return environment variable names local to a git repository."""
    return set(run_git(["rev-parse", "--local-env-vars"], timeout=timeout).split())


def worktree_files(
    root: Path, name: str, timeout: int = GIT_TIMEOUT_SECONDS
) -> list[Path]:
    """List tracked and untracked files called name under root that git does not ignore."""
    # Local git variables must not redirect the index or working tree.
    local_vars = local_env_names(timeout=timeout)
    env = {key: value for key, value in os.environ.items() if key not in local_vars}
    # git skips ignored paths, so linked worktrees under .claude/worktrees/ stay out.
    stdout = run_git(
        [
            "ls-files",
            "-z",
            "--cached",
            "--others",
            "--exclude-standard",
            "--",
            name,
            f"**/{name}",
        ],
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
