#!/usr/bin/env python3

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Verify OCI image manifest, index manifest, or image config release identity."""

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
REVISION = re.compile(r"^[0-9a-f]{40}$")
ALLOWED_CONFIG_PLATFORMS = frozenset({"linux/amd64", "linux/arm64"})


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
    manifest: dict[str, Any],
    raw: bytes,
    expected: str,
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


def verify_index_digest(raw: bytes, expected: str) -> None:
    """Reject an index whose raw bytes do not hash to the expected digest."""
    actual = "sha256:" + hashlib.sha256(raw).hexdigest()
    if actual != expected:
        raise ValueError(
            f"index digest mismatch: got {actual}, expected {expected}",
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
        platform = entry.get("platform")
        if not isinstance(platform, dict):
            raise ValueError("manifest list entry has no valid platform object")
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


def verify_config(
    data: dict[str, Any],
    platform: str,
    source: str,
    revision: str,
    version: str,
) -> None:
    """Verify one image config matches platform, source, revision, and version."""
    if platform not in ALLOWED_CONFIG_PLATFORMS:
        raise ValueError(f"unsupported platform: {platform!r}")
    if REVISION.fullmatch(revision) is None:
        raise ValueError(f"revision is not a 40-character hex SHA-1: {revision!r}")
    os_name = data.get("os")
    architecture = data.get("architecture")
    if not isinstance(os_name, str) or not isinstance(architecture, str):
        raise ValueError("image config missing valid os or architecture")
    actual_platform = f"{os_name}/{architecture}"
    if actual_platform != platform:
        raise ValueError(
            f"platform mismatch: got {actual_platform!r}, expected {platform!r}",
        )
    config = data.get("config")
    if not isinstance(config, dict):
        raise ValueError("image config has no valid config object")
    labels = config.get("Labels")
    if not isinstance(labels, dict):
        raise ValueError("image config Labels is missing or not an object")
    source_label = labels.get("org.opencontainers.image.source")
    if source_label != source:
        raise ValueError(
            f"source label mismatch: got {source_label!r}, expected {source!r}",
        )
    revision_label = labels.get("org.opencontainers.image.revision")
    if revision_label != revision:
        raise ValueError(
            f"revision label mismatch: got {revision_label!r}, expected {revision!r}",
        )
    version_label = labels.get("org.opencontainers.image.version")
    if version_label != version:
        raise ValueError(
            f"version label mismatch: got {version_label!r}, expected {version!r}",
        )


def print_usage() -> None:
    """Print command-line usage across all supported execution modes."""
    print(
        f"usage: {sys.argv[0]} EXPECTED_IMAGE_ID [--index]\n"
        f"       {sys.argv[0]} --config PLATFORM SOURCE REVISION VERSION",
        file=sys.stderr,
    )


def main() -> int:
    args = sys.argv[1:]
    if "--config" in args:
        if len(args) != 5 or args[0] != "--config":
            print_usage()
            return 2
        platform, source, revision, version = (
            args[1],
            args[2],
            args[3],
            args[4],
        )
        try:
            config_data, _ = read_manifest()
            verify_config(config_data, platform, source, revision, version)
        except ValueError as exc:
            print(f"tiny trainer manifest: {exc}", file=sys.stderr)
            return 1
        print(platform)
        return 0

    index_mode = "--index" in args
    positional = [a for a in args if a != "--index"]
    if len(positional) != 1 or len(args) > 2:
        print_usage()
        return 2
    expected = positional[0]
    if DIGEST.fullmatch(expected) is None:
        print("expected image id is not a SHA-256 digest", file=sys.stderr)
        return 1
    try:
        manifest, raw = read_manifest()
        if index_mode:
            verify_index_digest(raw, expected)
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
