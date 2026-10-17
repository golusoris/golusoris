#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Runs every CLI binary goreleaser built and checks it reports the release tag."""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from collections.abc import Callable
from pathlib import Path

EXPECTED_BINARIES = ("golusoris", "golusoris-mcp")
TARGET_PATTERN = re.compile(r"^(linux|darwin|windows)/(amd64|arm64)$")
MAX_ARTIFACTS_BYTES = 4 * 1024 * 1024
MAX_ARTIFACTS_ENTRIES = 1024
SUBPROCESS_TIMEOUT_SECONDS = 30


def run_binary(path: str, *, argv_prefix: tuple[str, ...] = ()) -> tuple[int, str]:
    # Run binary with --version and capture combined stdout and stderr.
    cmd = [*argv_prefix, path, "--version"]
    try:
        proc = subprocess.run(  # noqa: S603 - verified relative artifact path with fixed flag.
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            timeout=SUBPROCESS_TIMEOUT_SECONDS,
            check=False,
        )
        return proc.returncode, proc.stdout
    except (subprocess.TimeoutExpired, OSError) as exc:
        return -1, str(exc)


def load_artifacts(artifacts_path: Path) -> list[dict[str, object]] | None:
    # Read and validate the artifacts list from goreleaser output.
    if not artifacts_path.is_file() or artifacts_path.stat().st_size == 0:
        print(f"release-smoke: {artifacts_path} missing or empty", file=sys.stderr)
        return None
    if artifacts_path.stat().st_size > MAX_ARTIFACTS_BYTES:
        print(f"release-smoke: {artifacts_path} exceeds 4 MiB", file=sys.stderr)
        return None
    try:
        data = json.loads(artifacts_path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        print(f"release-smoke: {artifacts_path} invalid JSON: {exc}", file=sys.stderr)
        return None
    if not isinstance(data, list):
        print(f"release-smoke: {artifacts_path} must be a JSON array", file=sys.stderr)
        return None
    if len(data) > MAX_ARTIFACTS_ENTRIES:
        print(f"release-smoke: {artifacts_path} exceeds 1024 entries", file=sys.stderr)
        return None
    return [item for item in data[:MAX_ARTIFACTS_ENTRIES] if isinstance(item, dict)]


def select_binary(
    artifacts: list[dict[str, object]],
    name: str,
    goos: str,
    goarch: str,
) -> tuple[str | None, str | None]:
    # Select exactly one matching binary path from artifacts for the target OS/arch.
    matches: list[str] = []
    for entry in artifacts[:MAX_ARTIFACTS_ENTRIES]:
        if (
            entry.get("type") == "Binary"
            and entry.get("goos") == goos
            and entry.get("goarch") == goarch
            and isinstance(entry.get("extra"), dict)
            and entry["extra"].get("ID") == name
        ):
            path_val = entry.get("path")
            if isinstance(path_val, str) and path_val:
                matches.append(path_val)

    if len(matches) != 1:
        return None, f"release-smoke: {name}: expected one {goos}/{goarch} binary, found {len(matches)}"

    path_str = matches[0]
    path_obj = Path(path_str)
    if path_obj.is_absolute() or ".." in path_obj.parts:
        return None, f"release-smoke: {path_str} must be relative and contain no '..'"

    return path_str, None


def verify_binary(
    path: str,
    name: str,
    want: str,
    runner: Callable[[str], tuple[int, str]],
) -> tuple[bool, str]:
    # Execute the binary and verify its version output format.
    try:
        status, raw_out = runner(path)
    except (subprocess.TimeoutExpired, OSError) as exc:
        status, raw_out = -1, str(exc)

    if status != 0:
        return False, f"release-smoke: {path} --version failed: {raw_out.strip()}"

    output = raw_out.strip()
    expected = f"{name} version {want}"
    if output != expected and not output.startswith(f"{expected} "):
        return False, f"release-smoke: {path} reports '{output}', want \"{expected}\""

    return True, f"release-smoke: {output}"


def check_release_binaries(
    dist: Path | str,
    target: str,
    want: str,
    runner: Callable[[str], tuple[int, str]] = run_binary,
) -> int:
    # Run smoke check for all expected binaries in dist on target.
    target_match = TARGET_PATTERN.match(target)
    if not target_match:
        print(f"release-smoke: invalid target '{target}'", file=sys.stderr)
        return 2

    goos, goarch = target_match.group(1), target_match.group(2)
    artifacts = load_artifacts(Path(dist) / "artifacts.json")
    if artifacts is None:
        return 1

    failures = 0
    for name in EXPECTED_BINARIES:
        path, select_err = select_binary(artifacts, name, goos, goarch)
        if select_err is not None:
            print(select_err, file=sys.stderr)
            failures += 1
            continue

        if path is None:
            failures += 1
            continue

        ok, message = verify_binary(path, name, want, runner)
        if not ok:
            print(message, file=sys.stderr)
            failures += 1
            continue

        print(message)

    if failures > 0:
        print(
            f"release-smoke: {failures} of {len(EXPECTED_BINARIES)} binaries failed",
            file=sys.stderr,
        )
        return 1

    return 0


def main(
    argv: list[str] | None = None,
    runner: Callable[[str], tuple[int, str]] = run_binary,
) -> int:
    # Parse arguments and run release smoke checks.
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True, help="release version or tag")
    parser.add_argument("--target", required=True, help="target OS/arch pair")
    parser.add_argument(
        "--dist",
        type=Path,
        default=Path("dist"),
        help="path to dist directory holding artifacts.json",
    )
    args = parser.parse_args(argv)

    raw_version = args.version
    want = raw_version.removeprefix("v")
    if not want:
        print(f"release-smoke: empty version in tag '{raw_version}'", file=sys.stderr)
        return 2

    if not TARGET_PATTERN.match(args.target):
        print(f"release-smoke: invalid target '{args.target}'", file=sys.stderr)
        return 2

    return check_release_binaries(args.dist, args.target, want, runner=runner)


if __name__ == "__main__":
    sys.exit(main())
