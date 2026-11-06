#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""Reject sidecar deployment documentation without immutable image references."""

from __future__ import annotations

import re
import sys
from pathlib import Path

IMAGE = "ghcr.io/golusoris/tiny-litert-sidecar"
REFERENCE = re.compile(re.escape(IMAGE) + r"""[^\s\x60"'<>)]*""")
PINNED = re.compile(re.escape(IMAGE) + r"@sha256:[0-9a-f]{64}")
README = Path(__file__).resolve().parents[2] / "ai/tiny/serve/tflite/sidecar/README.md"


def check(text: str) -> None:
    """Require every sidecar image mention to be an exact SHA-256 reference."""
    references = REFERENCE.findall(text)
    if not references:
        raise ValueError("missing digest-pinned sidecar image reference")
    for reference in references:
        if PINNED.fullmatch(reference) is None:
            raise ValueError(f"sidecar image must use @sha256:<64 hex>: {reference}")


def check_fixtures() -> None:
    """Exercise clean, mutable, mixed, absent and malformed references."""
    pinned = IMAGE + "@sha256:" + "a" * 64
    check(f"{pinned}\n{pinned}\n")
    for reference in (
        IMAGE,
        IMAGE + ":latest",
        IMAGE + ":v0.1.1",
        IMAGE + ":v0.1.1@sha256:" + "a" * 64,
        IMAGE + "@sha256:" + "a" * 63,
        IMAGE + "@sha256:" + "a" * 65,
        IMAGE + "@sha256:" + "g" * 64,
        "",
    ):
        try:
            check(reference)
        except ValueError:
            continue
        raise AssertionError(f"planted reference accepted: {reference!r}")
    try:
        check(pinned + "\n" + IMAGE + ":latest")
    except ValueError:
        return
    raise AssertionError("planted mutable reference hidden by a valid digest")


def main() -> int:
    """Check the repository README or an explicitly supplied planted fixture."""
    try:
        if len(sys.argv) > 2:
            raise ValueError("usage: tiny-litert-sidecar-docs-policy_test.py [README]")
        path = Path(sys.argv[1]) if len(sys.argv) == 2 else README
        check(path.read_text(encoding="utf-8"))
        check_fixtures()
    except (OSError, ValueError, AssertionError) as exc:
        print(f"FAIL: {exc}", file=sys.stderr)
        return 1
    print("sidecar deployment docs policy: clean + planted fixtures PASS")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
