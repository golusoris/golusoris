#!/usr/bin/env python3

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Require one bounded OCI image manifest for the tested config digest."""

from __future__ import annotations

import json
import re
import sys
from typing import Any

MAX_MANIFEST_BYTES = 1 << 20
IMAGE_MEDIA_TYPES = {
    "application/vnd.oci.image.manifest.v1+json",
    "application/vnd.docker.distribution.manifest.v2+json",
}
DIGEST = re.compile(r"sha256:[a-f0-9]{64}")


def read_manifest() -> dict[str, Any]:
    """Read one bounded JSON object from standard input."""
    encoded = sys.stdin.buffer.read(MAX_MANIFEST_BYTES + 1)
    if len(encoded) > MAX_MANIFEST_BYTES:
        raise ValueError(f"manifest exceeds {MAX_MANIFEST_BYTES} bytes")
    try:
        value = json.loads(encoded)
    except (UnicodeError, json.JSONDecodeError) as exc:
        raise ValueError("manifest is not one valid UTF-8 JSON value") from exc
    if not isinstance(value, dict):
        raise ValueError("manifest must be a JSON object")
    return value


def verify_manifest(manifest: dict[str, Any], expected_config: str) -> None:
    """Reject indexes and require the exact tested image config digest."""
    if DIGEST.fullmatch(expected_config) is None:
        raise ValueError("expected config is not a SHA-256 digest")
    media_type = manifest.get("mediaType")
    if media_type not in IMAGE_MEDIA_TYPES or "manifests" in manifest:
        raise ValueError(f"remote object is not one image manifest: {media_type!r}")
    config = manifest.get("config")
    if not isinstance(config, dict) or config.get("digest") != expected_config:
        raise ValueError("remote manifest config digest differs from tested image")


def main() -> int:
    if len(sys.argv) != 2:
        print(f"usage: {sys.argv[0]} EXPECTED_CONFIG_DIGEST", file=sys.stderr)
        return 2
    try:
        verify_manifest(read_manifest(), sys.argv[1])
    except ValueError as exc:
        print(f"tiny trainer manifest: {exc}", file=sys.stderr)
        return 1
    print(sys.argv[1])
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
