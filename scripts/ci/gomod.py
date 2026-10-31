# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Extract module dependencies with Go's official go.mod parser."""

from __future__ import annotations

import json
import subprocess
from dataclasses import dataclass
from pathlib import Path

MAX_ENTRIES = 10_000


class GoModError(Exception):
    """Raised when Go cannot read or parse a go.mod file."""


@dataclass(frozen=True)
class GoMod:
    """Required module paths and replacement targets in source order."""

    requires: frozenset[str]
    replace_targets: tuple[str, ...]


def read_go_mod(path: Path) -> GoMod:
    """Read module JSON through Go, bounding dependency extraction."""
    resolved = path.resolve()
    if resolved.name != "go.mod" or not resolved.is_file():
        raise GoModError(f"could not read {path}: not a regular go.mod file")
    try:
        proc = subprocess.run(  # noqa: S603 - fixed Go command without shell execution.
            ["go", "mod", "edit", "-json", str(resolved)],  # noqa: S607 - setup-go supplies Go on PATH.
            capture_output=True,
            text=True,
            timeout=30,
            check=False,
        )
    except (OSError, subprocess.SubprocessError, UnicodeError) as error:
        raise GoModError(f"could not read {path}: {error}") from error
    if proc.returncode != 0:
        raise GoModError(
            f"go mod edit failed for {path} (exit {proc.returncode}): {proc.stderr.strip()}"
        )
    try:
        data = json.loads(proc.stdout)
        requires = data.get("Require") or []
        replaces = data.get("Replace") or []
        if not isinstance(requires, list) or not isinstance(replaces, list):
            raise GoModError(f"invalid dependency lists from Go for {path}")
        if len(requires) + len(replaces) > MAX_ENTRIES:
            raise GoModError(f"entry bound exceeded for {path}: max {MAX_ENTRIES}")
        return GoMod(
            frozenset(item["Path"] for item in requires),
            tuple(item["New"]["Path"] for item in replaces),
        )
    except (ValueError, TypeError, KeyError, AttributeError) as error:
        raise GoModError(f"invalid module JSON from Go for {path}: {error}") from error
