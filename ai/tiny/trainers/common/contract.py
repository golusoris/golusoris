# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""Bounded input and output contract shared by tiny trainer images."""

from __future__ import annotations

import json
import os
import shutil
import stat
import tarfile
import tempfile
import zipfile
from collections.abc import Mapping
from pathlib import Path
from typing import Any, BinaryIO
from urllib.parse import SplitResult, unquote, urlsplit

INPUT_ROOT = Path("/work/input")
OUTPUT_ROOT = Path("/work/output")
MAX_CONFIG_BYTES = 1 << 20
MAX_ARCHIVE_MEMBERS = 100_000
COPY_CHUNK_BYTES = 1 << 20


class ContractError(ValueError):
    """Input violates the trainer container contract."""


def load_config(path: Path = INPUT_ROOT / "config.json") -> dict[str, Any]:
    """Load a bounded JSON object and validate the shared fields."""
    data = _read_bounded(path, MAX_CONFIG_BYTES)
    try:
        config = json.loads(data)
    except json.JSONDecodeError as exc:
        raise ContractError(f"config.json is invalid JSON: {exc.msg}") from exc
    if not isinstance(config, dict):
        raise ContractError("config.json must contain an object")
    required_string(config, "dataset_uri")
    limit = config.get("max_dataset_bytes")
    if isinstance(limit, bool) or not isinstance(limit, int) or limit <= 0:
        raise ContractError("max_dataset_bytes must be a positive integer")
    return config


def required_string(config: Mapping[str, Any], name: str) -> str:
    """Return one required non-empty string field."""
    value = config.get(name)
    if not isinstance(value, str) or not value.strip():
        raise ContractError(f"{name} must be a non-empty string")
    return value


def object_field(config: Mapping[str, Any], name: str) -> dict[str, Any]:
    """Return one optional object field."""
    value = config.get(name, {})
    if value is None:
        return {}
    if not isinstance(value, dict) or not all(isinstance(key, str) for key in value):
        raise ContractError(f"{name} must be an object with string keys")
    return value


def materialize_dataset(
    config: Mapping[str, Any], workspace: Path, input_root: Path = INPUT_ROOT
) -> Path:
    """Resolve one staged file URI into a bounded regular file."""
    del workspace
    raw_uri = required_string(config, "dataset_uri")
    parsed = urlsplit(raw_uri)
    _validate_uri(parsed)
    max_bytes = config["max_dataset_bytes"]
    if not isinstance(max_bytes, int):
        raise ContractError("max_dataset_bytes must be an integer")
    return _validated_local_path(parsed, input_root, max_bytes)


def safe_extract_archive(source: Path, destination: Path, fmt: str, max_bytes: int) -> None:
    """Extract an image-folder archive without links, traversal, or expansion bombs."""
    destination.mkdir(mode=0o700, parents=True, exist_ok=False)
    if fmt == "zip":
        _extract_zip(source, destination, max_bytes)
        return
    if fmt in {"tar", "tar.gz", "tgz"}:
        _extract_tar(source, destination, fmt, max_bytes)
        return
    raise ContractError(f"unsupported archive format: {fmt}")


def atomic_write_json(path: Path, value: Mapping[str, Any]) -> None:
    """Write a JSON sidecar without exposing a partial output."""
    path.parent.mkdir(mode=0o755, parents=True, exist_ok=True)
    payload = json.dumps(value, sort_keys=True, separators=(",", ":"), allow_nan=False)
    handle = tempfile.NamedTemporaryFile(
        mode="w", encoding="utf-8", dir=path.parent, prefix=f".{path.name}.", delete=False
    )
    temporary = Path(handle.name)
    try:
        with handle:
            handle.write(payload)
            handle.flush()
            os.fsync(handle.fileno())
        temporary.chmod(0o644)
        temporary.replace(path)
    except Exception:
        temporary.unlink(missing_ok=True)
        raise


def _read_bounded(path: Path, max_bytes: int) -> bytes:
    try:
        info = path.stat()
    except OSError as exc:
        raise ContractError(f"cannot stat {path.name}: {exc}") from exc
    if not stat.S_ISREG(info.st_mode):
        raise ContractError(f"{path.name} must be a regular file")
    if info.st_size > max_bytes:
        raise ContractError(f"{path.name} exceeds {max_bytes} bytes")
    try:
        return path.read_bytes()
    except OSError as exc:
        raise ContractError(f"cannot read {path.name}: {exc}") from exc


def _validate_uri(parsed: SplitResult) -> None:
    if parsed.username or parsed.password or parsed.fragment or parsed.query:
        raise ContractError("dataset_uri must not contain credentials, query, or fragment")
    if parsed.scheme != "file":
        raise ContractError("dataset_uri scheme must be file; stage remote data before training")


def _validated_local_path(parsed: SplitResult, input_root: Path, max_bytes: int) -> Path:
    if parsed.netloc not in {"", "localhost"}:
        raise ContractError("file dataset_uri host must be empty or localhost")
    path = Path(os.path.abspath(unquote(parsed.path)))
    root = input_root.resolve(strict=True)
    if not path.is_relative_to(root):
        raise ContractError("file dataset_uri must remain under /work/input")
    relative = path.relative_to(root)
    current = root
    for component in relative.parts:
        current /= component
        if current.is_symlink():
            raise ContractError("file dataset_uri must not contain symbolic links")
    path = path.resolve(strict=True)
    if not path.is_relative_to(root):
        raise ContractError("file dataset_uri must remain under /work/input")
    info = path.stat()
    if not stat.S_ISREG(info.st_mode):
        raise ContractError("dataset must be a regular file")
    if info.st_size > max_bytes:
        raise ContractError(f"dataset exceeds {max_bytes} bytes")
    return path


def _copy_bounded(source: BinaryIO, destination: BinaryIO, max_bytes: int) -> int:
    copied = 0
    max_chunks = max_bytes // COPY_CHUNK_BYTES + 2
    for _ in range(max_chunks):
        chunk = source.read(min(COPY_CHUNK_BYTES, max_bytes - copied + 1))
        if not chunk:
            return copied
        copied += len(chunk)
        if copied > max_bytes:
            raise ContractError(f"dataset expands beyond {max_bytes} bytes")
        destination.write(chunk)
    raise ContractError("bounded copy exhausted its iteration limit")


def _safe_member_path(root: Path, name: str) -> Path:
    if not name or "\x00" in name:
        raise ContractError("archive contains an empty or NUL path")
    target = (root / name).resolve()
    if not target.is_relative_to(root.resolve()):
        raise ContractError(f"archive path escapes extraction root: {name}")
    return target


def _extract_zip(source: Path, destination: Path, max_bytes: int) -> None:
    expanded = 0
    with zipfile.ZipFile(source) as archive:
        members = archive.infolist()
        if len(members) > MAX_ARCHIVE_MEMBERS:
            raise ContractError("archive has too many members")
        for member in members:
            target = _safe_member_path(destination, member.filename)
            member_mode = member.external_attr >> 16
            if stat.S_ISLNK(member_mode):
                raise ContractError(f"archive links are not allowed: {member.filename}")
            if member.is_dir():
                target.mkdir(mode=0o755, parents=True, exist_ok=True)
                continue
            expanded += member.file_size
            if expanded > max_bytes:
                raise ContractError(f"archive expands beyond {max_bytes} bytes")
            target.parent.mkdir(mode=0o755, parents=True, exist_ok=True)
            with archive.open(member) as reader, target.open("xb") as writer:
                _copy_bounded(reader, writer, member.file_size)
            target.chmod(0o644)


def _extract_tar(source: Path, destination: Path, fmt: str, max_bytes: int) -> None:
    expanded = 0
    mode = "r:gz" if fmt in {"tar.gz", "tgz"} else "r:"
    with tarfile.open(source, mode=mode) as archive:
        for index in range(MAX_ARCHIVE_MEMBERS + 1):
            member = archive.next()
            if member is None:
                return
            if index == MAX_ARCHIVE_MEMBERS:
                raise ContractError("archive has too many members")
            target = _safe_member_path(destination, member.name)
            if member.isdir():
                target.mkdir(mode=0o755, parents=True, exist_ok=True)
                continue
            if not member.isfile():
                raise ContractError(f"archive links and special files are not allowed: {member.name}")
            expanded += member.size
            if expanded > max_bytes:
                raise ContractError(f"archive expands beyond {max_bytes} bytes")
            reader = archive.extractfile(member)
            if reader is None:
                raise ContractError(f"archive member cannot be read: {member.name}")
            target.parent.mkdir(mode=0o755, parents=True, exist_ok=True)
            with reader, target.open("xb") as writer:
                _copy_bounded(reader, writer, member.size)
            target.chmod(0o644)
    raise ContractError("tar archive ended without an end marker")


def remove_tree(path: Path) -> None:
    """Remove a private workspace after restoring owner permissions."""
    if path.exists():
        shutil.rmtree(path)
