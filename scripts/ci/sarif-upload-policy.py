#!/usr/bin/env python3

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Refuse a SARIF upload step that fails open on a public repository."""

from __future__ import annotations

import sys
from pathlib import Path

UPLOAD = "github/codeql-action/upload-sarif@"
ALLOWED = {"false", "${{ github.event.repository.private }}"}
MAX_LINES = 20_000


def step_blocks(lines: list[str]) -> list[tuple[int, list[str]]]:
    """Split workflow lines into step blocks keyed by their first line number."""
    blocks: list[tuple[int, list[str]]] = []
    start, indent, body = 0, -1, []
    for number, line in enumerate(lines, 1):
        stripped = line.lstrip(" ")
        depth = len(line) - len(stripped)
        if stripped.startswith("- ") and (indent < 0 or depth <= indent):
            if body:
                blocks.append((start, body))
            start, indent, body = number, depth, [line]
        elif indent >= 0 and stripped and depth <= indent:
            blocks.append((start, body))
            start, indent, body = 0, -1, []
        elif indent >= 0:
            body.append(line)
    if body:
        blocks.append((start, body))
    return blocks


def violations(path: Path) -> list[str]:
    lines = path.read_text(encoding="utf-8").splitlines()
    if len(lines) > MAX_LINES:
        return [f"{path}: {len(lines)} lines exceed bound {MAX_LINES}"]
    found = []
    for start, body in step_blocks(lines):
        if not any(UPLOAD in line for line in body):
            continue
        for line in body:
            key, _, value = line.strip().partition(":")
            if key == "continue-on-error" and value.split("#")[0].strip() not in ALLOWED:
                found.append(f"{path}:{start}: SARIF upload has continue-on-error {value.strip()}")
    return found


def main(argv: list[str]) -> int:
    root = Path(argv[1]) if len(argv) > 1 else Path(".github/workflows")
    files = sorted([*root.glob("*.yml"), *root.glob("*.yaml")])
    if not files:
        print(f"sarif upload policy: no workflow files under {root}", file=sys.stderr)
        return 1
    found = [item for path in files for item in violations(path)]
    for item in found:
        print(item, file=sys.stderr)
    if found:
        return 1
    uploads = sum(path.read_text(encoding="utf-8").count(UPLOAD) for path in files)
    if uploads == 0:
        print(f"sarif upload policy: no upload-sarif step under {root}", file=sys.stderr)
        return 1
    print(f"sarif upload policy: {uploads} upload steps fail closed")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
