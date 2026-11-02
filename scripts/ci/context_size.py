#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Fail when an AGENTS.md exceeds a coding client's context cap.

Windsurf reads each rule file up to 12,000 characters; Codex reads the AGENTS.md files on the
path from the repository root to the working directory up to 32 KiB together. A file past
either cap is silently truncated for that client.
"""

from __future__ import annotations

import shutil
import subprocess
import sys
from pathlib import Path

WINDSURF_CHARS_PER_FILE = 12_000
CODEX_BYTES_PER_CHAIN = 32 * 1024


def tracked_agents_files(root: Path) -> list[str]:
    """Return every tracked or new AGENTS.md path, relative to root."""
    git = shutil.which("git")
    if git is None:
        raise SystemExit("context-size: git is not on PATH")
    listed = subprocess.run(  # noqa: S603 - fixed git command resolved on PATH.
        [git, "ls-files", "--cached", "--others", "--exclude-standard", "--", "*AGENTS.md"],
        cwd=root,
        capture_output=True,
        text=True,
        check=True,
        timeout=60,
    )
    return sorted(set(listed.stdout.split()))


def _chain(path: str) -> list[str]:
    """Return the AGENTS.md candidates from the root down to the directory holding path."""
    parts = Path(path).parent.parts
    return [Path(*parts[:depth], "AGENTS.md").as_posix() for depth in range(len(parts) + 1)]


def oversized(root: Path, files: list[str]) -> list[str]:
    """Return one message per AGENTS.md or AGENTS.md chain past a client cap."""
    present = set(files)
    sizes = {f: (root / f).read_bytes() for f in files}
    problems = []
    for name in files:
        chars = len(sizes[name].decode("utf-8"))
        if chars > WINDSURF_CHARS_PER_FILE:
            problems.append(f"{name}: {chars} chars > Windsurf cap {WINDSURF_CHARS_PER_FILE}")
        chain_bytes = sum(len(sizes[c]) for c in _chain(name) if c in present)
        if chain_bytes > CODEX_BYTES_PER_CHAIN:
            problems.append(f"{name}: chain {chain_bytes} B > Codex cap {CODEX_BYTES_PER_CHAIN}")
    return problems


def main(argv: list[str]) -> int:
    """Check the repository at argv[1] (default: current directory)."""
    root = Path(argv[1] if len(argv) > 1 else ".").resolve()
    problems = oversized(root, tracked_agents_files(root))
    for line in problems:
        sys.stderr.write(f"context-size: {line}; move bulk to linked on-demand docs\n")
    return 1 if problems else 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
