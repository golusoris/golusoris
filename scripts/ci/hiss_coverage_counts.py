#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Fail when the HISS-20 rationale states claim counts the catalogue no longer has."""

from __future__ import annotations

import re
import sys
from dataclasses import dataclass
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
CATALOGUE = ROOT / ".config/hiss/coverage.yaml"
MAX_LINES = 20_000

INVARIANT = re.compile(r"^  - id: HISS-\d{2}$")
CLAIM = re.compile(r"^      - language: \S+$")
RUNNER = re.compile(r"^        runner: \S+$")
STATED = re.compile(
    r"of the (\d+) claims declared here only (\d+) are replayed by the verifier;"
    r" the other (\d+) name a tool"
)


@dataclass(frozen=True)
class Counts:
    """Claim totals as the verifier splits them."""

    claims: int
    replayed: int
    delegated: int


def measure(lines: list[str]) -> Counts:
    """Count claims and delegated claims; a claim outside an invariant fails closed."""
    if len(lines) > MAX_LINES:
        raise ValueError(f"catalogue exceeds {MAX_LINES} lines")
    claims = delegated = 0
    in_invariant = False
    for line in lines:
        if INVARIANT.match(line):
            in_invariant = True
        elif CLAIM.match(line):
            if not in_invariant:
                raise ValueError(f"claim outside an invariant: {line.strip()}")
            claims += 1
        elif RUNNER.match(line):
            delegated += 1
    return Counts(claims, claims - delegated, delegated)


def stated(text: str) -> Counts:
    """Read the counts the HISS-20 rationale states."""
    found = STATED.findall(" ".join(text.split()))
    if len(found) != 1:
        raise ValueError(f"expected one stated claim count, found {len(found)}")
    claims, replayed, delegated = (int(value) for value in found[0])
    return Counts(claims, replayed, delegated)


def check(text: str) -> list[str]:
    """Return one error per mismatch between stated and measured counts."""
    try:
        measured = measure(text.splitlines())
        declared = stated(text)
    except ValueError as err:
        return [str(err)]
    if measured == declared:
        return []
    return [f"HISS-20 rationale states {declared}, catalogue has {measured}"]


def main(argv: list[str]) -> int:
    """Check the catalogue named on the command line, or the repository one."""
    path = Path(argv[1]) if len(argv) > 1 else CATALOGUE
    errors = check(path.read_text(encoding="utf-8"))
    for error in errors:
        sys.stderr.write(f"hiss-coverage-counts: {path}: {error}\n")
    if not errors:
        sys.stdout.write(f"hiss-coverage-counts: {path}: stated counts match\n")
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
