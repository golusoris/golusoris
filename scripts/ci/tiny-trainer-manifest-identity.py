#!/usr/bin/env python3

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Verify OCI image or index manifest matches a tested image identity."""

from __future__ import annotations

import hashlib
import json
import re
import sys
from typing import Any

MAX_MANIFEST_BYTES = 1 << 20
IMAGE_MEDIA_TYPES = {
    "application/vnd.oci.image.manifest.v1+json",
    "application/vnd.docker.distribution.manifest.v2+json",
}
INDEX_MEDIA_TYPES = {
    "application/vnd.oci.image.index.v1+json",
    "application/vnd.docker.distribution.manifest.list.v2+json",
}
DIGEST = re.compile(r"sha256:[a-f0-9]{64}")


def read_manifest() -> tuple[dict[str, Any], bytes]:
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
    return value, encoded


def verify_single_manifest(
    manifest: dict[str, Any], raw: bytes, expected: str,
) -> None:
    """Reject indexes and require the tested config or manifest digest."""
    media_type = manifest.get("mediaType")
    if media_type not in IMAGE_MEDIA_TYPES or "manifests" in manifest:
        raise ValueError(f"remote object is not one image manifest: {media_type!r}")
    config = manifest.get("config")
    config_matches = isinstance(config, dict) and config.get("digest") == expected
    raw_matches = "sha256:" + hashlib.sha256(raw).hexdigest() == expected
    if not (config_matches or raw_matches):
        raise ValueError(
            "remote manifest matches neither tested config nor manifest digest",
        )


def verify_index(manifest: dict[str, Any]) -> None:
    """Verify a manifest list contains both required platform entries."""
    media_type = manifest.get("mediaType")
    if media_type not in INDEX_MEDIA_TYPES:
        raise ValueError(f"remote object is not a manifest list: {media_type!r}")
    manifests = manifest.get("manifests")
    if not isinstance(manifests, list) or len(manifests) == 0:
        raise ValueError("manifest list has no entries")
    platforms_seen: list[str] = []
    for entry in manifests:
        if not isinstance(entry, dict):
            raise ValueError("manifest list entry is not an object")
        digest = entry.get("digest")
        if not isinstance(digest, str) or DIGEST.fullmatch(digest) is None:
            raise ValueError(f"manifest list entry has invalid digest: {digest!r}")
        platform = entry.get("platform", {})
        arch = platform.get("architecture", "")
        os_name = platform.get("os", "")
        platforms_seen.append(f"{os_name}/{arch}")
    required = {"linux/amd64", "linux/arm64"}
    found = set(platforms_seen)
    missing = required - found
    if missing:
        raise ValueError(
            f"manifest list missing required platforms: {', '.join(sorted(missing))}; "
            f"found: {', '.join(sorted(found))}",
        )


def main() -> int:
    if len(sys.argv) < 2 or len(sys.argv) > 3:
        print(
            f"usage: {sys.argv[0]} EXPECTED_IMAGE_ID [--index]",
            file=sys.stderr,
        )
        return 2
    index_mode = "--index" in sys.argv
    expected = sys.argv[1] if sys.argv[1] != "--index" else sys.argv[2]
    if DIGEST.fullmatch(expected) is None:
        print("expected image id is not a SHA-256 digest", file=sys.stderr)
        return 1
    try:
        manifest, raw = read_manifest()
        if index_mode:
            verify_index(manifest)
        else:
            verify_single_manifest(manifest, raw, expected)
    except ValueError as exc:
        print(f"tiny trainer manifest: {exc}", file=sys.stderr)
        return 1
    print(expected)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
