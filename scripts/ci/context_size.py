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
import subprocess  # Runs only fixed git subcommands below. # nosec B404
import sys
from pathlib import Path, PurePosixPath

NAME = "AGENTS.md"
WINDSURF_CHARS_PER_FILE = 12_000
CODEX_BYTES_PER_CHAIN = 32 * 1024


class CheckError(Exception):
    """The check could not run; main reports it with exit status 3."""


def _git(root: Path, *args: str, stdin: bytes | None = None) -> bytes:
    """Run one fixed git command in root and return its stdout."""
    git = shutil.which("git")
    if git is None:
        raise CheckError("git is not on PATH")
    if not root.is_dir():
        raise CheckError(f"{root} is not a directory")
    done = subprocess.run(  # noqa: S603 - fixed git subcommands; root checked above. # nosec B603
        [git, *args], cwd=root, input=stdin, capture_output=True, check=True, timeout=60
    )
    return done.stdout


def _is_agents(path: str) -> bool:
    """Return whether path names a file called exactly AGENTS.md."""
    return PurePosixPath(path).name == NAME


def _staged_blobs(root: Path) -> dict[str, str]:
    """Map each staged AGENTS.md path to its index object ID."""
    raw = _git(root, "ls-files", "-z", "--stage", "--", f"*{NAME}")
    blobs = {}
    for entry in filter(None, raw.split(b"\0")):
        meta, _, path = entry.decode("utf-8").partition("\t")
        if _is_agents(path):
            blobs[path] = meta.split()[1]
    return blobs


def _read_blobs(root: Path, oids: list[str]) -> list[bytes]:
    """Read blobs by object ID; IDs are hex, so newline-separated batch input is safe."""
    batch = _git(root, "cat-file", "--batch", stdin="".join(f"{oid}\n" for oid in oids).encode())
    blobs = []
    for oid in oids:
        header, _, batch = batch.partition(b"\n")
        fields = header.split()
        if len(fields) != 3 or fields[0].decode() != oid:
            raise CheckError(f"git cat-file returned {header!r} for {oid}")
        size = int(fields[2])
        blobs.append(batch[:size])
        batch = batch[size + 1 :]
    return blobs


def agents_contents(root: Path, *, worktree: bool = False) -> dict[str, bytes]:
    """Return every AGENTS.md as the next commit holds it, or with worktree=True as on disk.

    Commit view: tracked files from the index, untracked ones with the LF endings that
    eol=lf gives them on add. Disk view: raw bytes, since clients read those.
    """
    staged = _staged_blobs(root)
    paths = sorted(staged)
    if worktree:
        contents = {p: (root / p).read_bytes() for p in paths if (root / p).is_file()}
    else:
        blobs = _read_blobs(root, [staged[p] for p in paths]) if paths else []
        contents = dict(zip(paths, blobs, strict=True))
    new = _git(root, "ls-files", "-z", "--others", "--exclude-standard", "--", f"*{NAME}")
    for path in sorted({p.decode("utf-8") for p in new.split(b"\0") if p}):
        if _is_agents(path):
            raw = (root / path).read_bytes()
            contents[path] = raw if worktree else raw.replace(b"\r\n", b"\n")
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
    """Check [--worktree] [root] (default: current directory): 0 fits, 1 oversized, 3 error.

    Without --worktree tracked files are read as staged (pre-commit); with it, as on disk.
    """
    args = argv[1:]
    worktree = "--worktree" in args
    rest = [a for a in args if a != "--worktree"]
    if len(rest) > 1:
        sys.stderr.write("context-size: error: usage: context_size.py [--worktree] [root]\n")
        return 3
    root = Path(rest[0] if rest else ".").resolve()
    try:
        problems = oversized(agents_contents(root, worktree=worktree))
    except (CheckError, OSError, UnicodeDecodeError, subprocess.SubprocessError) as err:
        sys.stderr.write(f"context-size: error: {err}\n")
        return 3
    for line in problems:
        sys.stderr.write(f"context-size: {line}; move bulk to linked on-demand docs\n")
    return 1 if problems else 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
