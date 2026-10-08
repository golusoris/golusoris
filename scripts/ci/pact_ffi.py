#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Install the Pact FFI release artifact only after pinned SHA-256 verification."""

from __future__ import annotations

import argparse
import gzip
import hashlib
import http.client
import os
import platform
import re
import subprocess
import sys
import tempfile
import urllib.request
from dataclasses import dataclass
from pathlib import Path
from typing import BinaryIO

PACT_GO_VERSION = "2.8.0"
FFI_VERSION = "0.5.8"
RELEASE_BASE = (
    "https://github.com/pact-foundation/pact-reference/releases/download/"
    f"libpact_ffi-v{FFI_VERSION}"
)
MAX_ARCHIVE_BYTES = 16 << 20
MAX_LIBRARY_BYTES = 64 << 20
CHUNK_BYTES = 64 << 10


class InstallError(RuntimeError):
    """Report a fail-closed Pact FFI installation error."""


@dataclass(frozen=True)
class Asset:
    """One immutable upstream release artifact."""

    filename: str
    sha256: str
    library: str


# Digests are GitHub release-asset SHA-256 values for libpact_ffi-v0.5.8.
ASSETS = {
    ("Linux", "x86_64"): Asset(
        "libpact_ffi-linux-x86_64.so.gz",
        "2e4e234bac79be7d934df7111f72d98c289c66e56ceda6ef568145ff73af20f4",
        "libpact_ffi.so",
    ),
    ("Linux", "aarch64"): Asset(
        "libpact_ffi-linux-aarch64.so.gz",
        "d5a9516d5e2937ae04e436b4e8ad61a8ebceb780359d79670c7e50b8501476d3",
        "libpact_ffi.so",
    ),
    ("Darwin", "x86_64"): Asset(
        "libpact_ffi-macos-x86_64.dylib.gz",
        "72a1bab279738a63931db0c13f3fcdf806c5fa17590a611c34330c3f881f6fd4",
        "libpact_ffi.dylib",
    ),
    ("Darwin", "aarch64"): Asset(
        "libpact_ffi-macos-aarch64.dylib.gz",
        "56eff246b0a72493d514dcc8a0d9ed9b8faf2745800dcf0689f5816a4206b472",
        "libpact_ffi.dylib",
    ),
    ("Windows", "x86_64"): Asset(
        "pact_ffi-windows-x86_64.dll.gz",
        "ed5d6614a18645d7b1a7244f16c50033f1555b0150a7bf25548adf1175af0094",
        "pact_ffi.dll",
    ),
    ("Windows", "aarch64"): Asset(
        "pact_ffi-windows-aarch64.dll.gz",
        "d3c11914d5eba5c9b78ea94aa681413fe517ec14c3eafe577aa818bff3e0a265",
        "pact_ffi.dll",
    ),
}


def normalized_arch(machine: str) -> str:
    """Map hosted-runner architecture names to upstream asset names."""
    aliases = {
        "amd64": "x86_64",
        "x64": "x86_64",
        "x86_64": "x86_64",
        "aarch64": "aarch64",
        "arm64": "aarch64",
    }
    try:
        return aliases[machine.lower()]
    except KeyError as error:
        raise InstallError(f"unsupported architecture: {machine}") from error


def select_asset(system_name: str, machine: str) -> Asset:
    """Select an exact asset or reject an unsupported host."""
    key = (system_name, normalized_arch(machine))
    try:
        return ASSETS[key]
    except KeyError as error:
        raise InstallError(f"unsupported Pact FFI host: {key!r}") from error


def verify_pact_go_pin(module: Path) -> None:
    """Reject dependency updates not paired with a reviewed FFI pin update."""
    if module.is_symlink() or not module.is_file():
        raise InstallError(f"Pact module is not a regular file: {module}")
    if module.stat().st_size > 64 << 10:
        raise InstallError(f"Pact module exceeds 65536 bytes: {module}")
    content = module.read_text(encoding="utf-8")
    match = re.search(
        r"(?m)^\s*(?:require\s+)?github\.com/pact-foundation/pact-go/v2\s+v([^\s]+)",
        content,
    )
    actual = match.group(1) if match else "missing"
    if actual != PACT_GO_VERSION:
        raise InstallError(
            f"pact-go pin is {actual}; expected v{PACT_GO_VERSION} for FFI v{FFI_VERSION}"
        )


def copy_bounded(source: BinaryIO, target: BinaryIO, limit: int, label: str) -> None:
    """Copy through a scalar byte and iteration bound."""
    total = 0
    chunks = (limit // CHUNK_BYTES) + 2
    for _ in range(chunks):
        chunk = source.read(CHUNK_BYTES)
        if not chunk:
            return
        total += len(chunk)
        if total > limit:
            raise InstallError(f"Pact FFI {label} exceeds {limit} bytes")
        target.write(chunk)
    raise InstallError(f"Pact FFI {label} exceeded bounded copy iterations")


def archive_digest(archive: Path) -> str:
    """Hash one bounded regular archive."""
    if archive.is_symlink() or not archive.is_file():
        raise InstallError(f"Pact FFI archive is not a regular file: {archive}")
    if archive.stat().st_size > MAX_ARCHIVE_BYTES:
        raise InstallError(f"Pact FFI archive exceeds {MAX_ARCHIVE_BYTES} bytes")
    digest = hashlib.sha256()
    with archive.open("rb") as source:
        for _ in range((MAX_ARCHIVE_BYTES // CHUNK_BYTES) + 2):
            chunk = source.read(CHUNK_BYTES)
            if not chunk:
                return digest.hexdigest()
            digest.update(chunk)
    raise InstallError("Pact FFI archive exceeded bounded hash iterations")


def download_archive(asset: Asset, directory: Path) -> Path:
    """Download one bounded archive and reject bytes outside the lock."""
    directory.mkdir(mode=0o700, parents=True, exist_ok=True)
    temporary = tempfile.NamedTemporaryFile(
        mode="wb", prefix=".pact-ffi-", suffix=".gz", dir=directory, delete=False
    )
    archive = Path(temporary.name)
    complete = False
    failure: InstallError | None = None
    try:
        url = f"{RELEASE_BASE}/{asset.filename}"
        with urllib.request.urlopen(url, timeout=60) as response:  # noqa: S310 - fixed HTTPS release origin and locked asset names.  # nosemgrep: python.lang.security.audit.dynamic-urllib-use-detected.dynamic-urllib-use-detected -- RELEASE_BASE is a fixed HTTPS origin and asset names come from the lock
            with temporary:
                copy_bounded(response, temporary, MAX_ARCHIVE_BYTES, "archive")
        actual = archive_digest(archive)
        if actual != asset.sha256:
            raise InstallError(
                f"Pact FFI digest mismatch: expected {asset.sha256}, got {actual}"
            )
        complete = True
    except InstallError as error:
        failure = error
    except (OSError, http.client.HTTPException) as error:
        failure = InstallError(f"Pact FFI download failed: {error}")
    cleanup_error = cleanup_temporary(temporary, archive, remove=not complete)
    raise_install_failure(failure, cleanup_error)
    return archive


def extract_archive(
    archive: Path,
    directory: Path,
    asset: Asset,
    *,
    max_library_bytes: int = MAX_LIBRARY_BYTES,
) -> Path:
    """Verify, bound, and atomically extract one FFI library."""
    actual = archive_digest(archive)
    if actual != asset.sha256:
        raise InstallError(
            f"Pact FFI digest mismatch: expected {asset.sha256}, got {actual}"
        )
    if Path(asset.library).name != asset.library:
        raise InstallError(f"invalid Pact FFI library name: {asset.library!r}")
    directory.mkdir(mode=0o700, parents=True, exist_ok=True)
    destination = directory / asset.library
    temporary = tempfile.NamedTemporaryFile(
        mode="wb", prefix=".pact-ffi-library-", dir=directory, delete=False
    )
    temporary_path = Path(temporary.name)
    failure: InstallError | None = None
    try:
        with gzip.open(archive, "rb") as source:
            with temporary:
                copy_bounded(source, temporary, max_library_bytes, "decompressed size")
        temporary_path.chmod(0o755)
        os.replace(temporary_path, destination)
    except InstallError as error:
        failure = error
    except (EOFError, OSError) as error:
        failure = InstallError(f"invalid Pact FFI gzip archive: {error}")
    cleanup_error = cleanup_temporary(temporary, temporary_path, remove=failure is not None)
    raise_install_failure(failure, cleanup_error)
    return destination


def cleanup_temporary(
    temporary: BinaryIO,
    path: Path,
    *,
    remove: bool,
) -> InstallError | None:
    """Close a temporary file and still attempt unlink after close failure."""
    failures: list[str] = []
    try:
        temporary.close()
    except OSError as error:
        failures.append(f"close {path.name}: {error}")
    if remove:
        try:
            path.unlink(missing_ok=True)
        except OSError as error:
            failures.append(f"unlink {path.name}: {error}")
    if failures:
        return InstallError("Pact FFI temporary cleanup failed: " + "; ".join(failures))
    return None


def raise_install_failure(
    failure: InstallError | None,
    cleanup_error: InstallError | None,
) -> None:
    """Raise one primary or cleanup failure without discarding either."""
    if failure is not None and cleanup_error is not None:
        raise InstallError(f"{failure}; {cleanup_error}") from failure
    if failure is not None:
        raise failure
    if cleanup_error is not None:
        raise cleanup_error


def fix_macos_install_name(system_name: str, library: Path) -> None:
    """Apply the same absolute install name required by pact-go upstream."""
    if system_name != "Darwin":
        return
    try:
        subprocess.run(  # noqa: S603 - fixed Apple tool and verified library path.
            ("/usr/bin/install_name_tool", "-id", str(library), str(library)),
            check=True,
            timeout=30,
        )
    except (OSError, subprocess.SubprocessError) as error:
        raise InstallError(f"failed to set Pact FFI install name: {error}") from error


def install(module: Path, library_dir: Path) -> Path:
    """Verify the dependency pin, then install the matching locked asset."""
    verify_pact_go_pin(module)
    system_name = platform.system()
    asset = select_asset(system_name, platform.machine())
    archive = download_archive(asset, library_dir)
    try:
        library = extract_archive(archive, library_dir, asset)
    finally:
        archive.unlink(missing_ok=True)
    fix_macos_install_name(system_name, library)
    return library


def main() -> int:
    """Parse bounded CI inputs and install the locked FFI artifact."""
    parser = argparse.ArgumentParser()
    parser.add_argument("--lib-dir", required=True, type=Path)
    parser.add_argument("--module", type=Path, default=Path("testutil/pact/go.mod"))
    arguments = parser.parse_args()
    try:
        library = install(arguments.module, arguments.lib_dir)
    except InstallError as error:
        print(f"Pact FFI install failed: {error}", file=sys.stderr)
        return 1
    print(f"installed verified Pact FFI v{FFI_VERSION}: {library}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
