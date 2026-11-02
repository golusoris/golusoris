#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Fail when an AGENTS.md exceeds a coding client's context cap.

Windsurf reads each rule file up to 12,000 characters; Codex reads the AGENTS.md files on the
path from the repository root to the working directory up to 32 KiB together. A file past
either cap is silently truncated for that client.

Tracked files are read from the index, so the check sees what the next commit holds, with
LF line endings, whatever the working tree or a Windows checkout shows.
"""

from __future__ import annotations

import shutil
import subprocess
import sys
from pathlib import Path, PurePosixPath

NAME = "AGENTS.md"
WINDSURF_CHARS_PER_FILE = 12_000
CODEX_BYTES_PER_CHAIN = 32 * 1024


def _git(root: Path, *args: str, stdin: bytes | None = None) -> bytes:
    """Run one fixed git command in root and return its stdout."""
    git = shutil.which("git")
    if git is None:
        raise SystemExit("context-size: git is not on PATH")
    if not root.is_dir():
        raise SystemExit(f"context-size: {root} is not a directory")
    done = subprocess.run(  # noqa: S603 - fixed git subcommands; root checked above.
        [git, *args], cwd=root, input=stdin, capture_output=True, check=True, timeout=60
    )
    return done.stdout


def _agents_paths(raw: bytes) -> list[str]:
    """Return the NUL-separated paths whose basename is exactly AGENTS.md."""
    paths = (p.decode("utf-8") for p in raw.split(b"\0") if p)
    return sorted({p for p in paths if PurePosixPath(p).name == NAME})


def agents_contents(root: Path) -> dict[str, bytes]:
    """Return every AGENTS.md: tracked ones as staged, untracked ones from disk."""
    staged = _git(root, "ls-files", "-z", "--cached", "--", f"*{NAME}")
    tracked = _agents_paths(staged)
    contents: dict[str, bytes] = {}
    if tracked:
        batch = _git(root, "cat-file", "--batch", stdin="".join(f":{p}\n" for p in tracked).encode())
        for path in tracked:
            header, _, batch = batch.partition(b"\n")
            size = int(header.split()[2])
            contents[path], batch = batch[:size], batch[size + 1 :]
    new = _git(root, "ls-files", "-z", "--others", "--exclude-standard", "--", f"*{NAME}")
    for path in _agents_paths(new):
        contents[path] = (root / path).read_bytes().replace(b"\r\n", b"\n")
    return contents


def _chain(path: str) -> list[str]:
    """Return the AGENTS.md candidates from the root down to the directory holding path."""
    parts = PurePosixPath(path).parent.parts
    return [PurePosixPath(*parts[:depth], NAME).as_posix() for depth in range(len(parts) + 1)]


def oversized(contents: dict[str, bytes]) -> list[str]:
    """Return one message per AGENTS.md or AGENTS.md chain past a client cap."""
    problems = []
    for name in sorted(contents):
        chars = len(contents[name].decode("utf-8"))
        if chars > WINDSURF_CHARS_PER_FILE:
            problems.append(f"{name}: {chars} chars > Windsurf cap {WINDSURF_CHARS_PER_FILE}")
        chain_bytes = sum(len(contents[c]) for c in _chain(name) if c in contents)
        if chain_bytes > CODEX_BYTES_PER_CHAIN:
            problems.append(f"{name}: chain {chain_bytes} B > Codex cap {CODEX_BYTES_PER_CHAIN}")
    return problems


def main(argv: list[str]) -> int:
    """Check the repository at argv[1] (default: current directory)."""
    root = Path(argv[1] if len(argv) > 1 else ".").resolve()
    problems = oversized(agents_contents(root))
    for line in problems:
        sys.stderr.write(f"context-size: {line}; move bulk to linked on-demand docs\n")
    return 1 if problems else 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
