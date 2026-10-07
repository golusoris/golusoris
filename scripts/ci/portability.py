#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Build, vet, and short-test every repository Go module on the current host."""

from __future__ import annotations

import argparse
import os
import re
import signal
import subprocess
import sys
import threading
import time
from dataclasses import dataclass
from pathlib import Path, PurePosixPath
from typing import BinaryIO

MAX_MODULES = 64
MAX_PACKAGES_PER_MODULE = 512
MAX_MANIFEST_OUTPUT_BYTES = 1 << 20
MAX_PROCESS_OUTPUT_BYTES = 1 << 20
PROCESS_READ_BYTES = 64 << 10
PROCESS_POLL_SECONDS = 0.05
PROCESS_TERMINATE_GRACE_SECONDS = 0.1
PROCESS_KILL_WAIT_SECONDS = 2.0
PROCESS_PIPE_JOIN_SECONDS = 1.0
PROCESS_TASKKILL_SECONDS = 5.0
MAX_PATH_COMPONENTS = 64
DISCOVERY_TIMEOUT_SECONDS = 60
LIST_TIMEOUT_SECONDS = 300
PHASE_TIMEOUT_SECONDS = 900
PHASES = (
    ("build", ("go", "build", "./...")),
    ("vet", ("go", "vet", "./...")),
    ("test-short", ("go", "test", "-short", "-count=1", "-timeout=10m", "./...")),
)


class PortabilityError(RuntimeError):
    """Report a fail-closed portability gate error."""


@dataclass(frozen=True)
class Summary:
    """Count work proven by one portability run."""

    modules: int
    packages: int
    phases: int


@dataclass(frozen=True)
class BoundedProcessOutput:
    """Bounded output and status collected from one child process."""

    returncode: int
    stdout: bytes
    stderr: bytes


class ProcessRunner:
    """Run fixed repository toolchain commands without a command shell."""

    def output(
        self,
        argv: tuple[str, ...],
        *,
        cwd: Path,
        environment: dict[str, str],
        timeout: int,
        max_output_bytes: int = MAX_PROCESS_OUTPUT_BYTES,
    ) -> bytes:
        if not 1 <= max_output_bytes <= MAX_PROCESS_OUTPUT_BYTES:
            raise PortabilityError(
                f"invalid command-output bound: {max_output_bytes}"
            )
        try:
            options: dict[str, object] = {
                "cwd": cwd,
                "env": environment,
                "stdout": subprocess.PIPE,
                "stderr": subprocess.PIPE,
                "bufsize": 0,
            }
            if os.name == "posix":
                options["start_new_session"] = True
            elif os.name == "nt":
                options["creationflags"] = subprocess.CREATE_NEW_PROCESS_GROUP
            process = subprocess.Popen(  # noqa: S603 - fixed Git/Go commands use runner PATH.
                argv,
                **options,
            )
        except OSError as error:
            raise PortabilityError(f"command failed to start {argv!r}: {error}") from error

        result = _collect_process_output(process, argv, timeout, max_output_bytes)
        if result.returncode != 0:
            detail = result.stderr.decode(errors="replace")[-4000:]
            raise PortabilityError(
                f"command failed with exit {result.returncode}: {argv!r}\n{detail}"
            )
        return result.stdout

    def run(
        self,
        argv: tuple[str, ...],
        *,
        cwd: Path,
        environment: dict[str, str],
        timeout: int,
    ) -> None:
        try:
            result = subprocess.run(  # noqa: S603 - fixed Go commands use runner PATH.
                argv,
                cwd=cwd,
                env=environment,
                check=False,
                timeout=timeout,
            )
        except (OSError, subprocess.TimeoutExpired) as error:
            raise PortabilityError(f"command failed to start {argv!r}: {error}") from error
        if result.returncode != 0:
            raise PortabilityError(f"command failed with exit {result.returncode}: {argv!r}")


def _read_process_pipe(
    stream: BinaryIO | None,
    output: bytearray,
    limit: int,
    overflow: list[bool],
    read_errors: list[OSError | None],
    index: int,
    failure_event: threading.Event,
) -> None:
    """Drain one child pipe while retaining at most limit bytes."""
    if stream is None:
        read_errors[index] = OSError("subprocess pipe was not created")
        failure_event.set()
        return
    try:
        for _ in range(limit + 2):
            remaining = limit - len(output)
            read_size = min(PROCESS_READ_BYTES, max(remaining+1, 1))
            chunk = os.read(stream.fileno(), read_size)
            if not chunk:
                return
            output.extend(chunk[:remaining])
            if len(chunk) > remaining:
                overflow[index] = True
                failure_event.set()
                return
        overflow[index] = True
        failure_event.set()
    except OSError as error:
        read_errors[index] = error
        failure_event.set()


def _collect_process_output(
    process: subprocess.Popen[bytes],
    argv: tuple[str, ...],
    timeout: int,
    limit: int,
) -> BoundedProcessOutput:
    """Drain both child pipes concurrently and enforce the hard byte bound."""
    buffers = (bytearray(), bytearray())
    overflow = [False, False]
    read_errors: list[OSError | None] = [None, None]
    failure_event = threading.Event()
    streams = (process.stdout, process.stderr)
    threads = [
        threading.Thread(
            target=_read_process_pipe,
            args=(stream, buffers[index], limit, overflow, read_errors, index, failure_event),
            daemon=True,
        )
        for index, stream in enumerate(streams)
    ]
    for thread in threads:
        thread.start()
    returncode, timed_out, termination_error = _wait_for_process(
        process, timeout, failure_event
    )
    pipe_error = _finish_process_readers(process, threads, streams, read_errors)
    _close_process_streams(streams, read_errors)
    _raise_process_output_error(
        argv,
        timeout,
        limit,
        timed_out,
        read_errors,
        overflow,
        termination_error,
        pipe_error,
    )
    return BoundedProcessOutput(returncode, bytes(buffers[0]), bytes(buffers[1]))


def _wait_for_process(
    process: subprocess.Popen[bytes],
    timeout: int,
    failure_event: threading.Event,
) -> tuple[int, bool, str]:
    """Poll for reader failure and bound process-tree termination."""
    deadline = time.monotonic() + timeout
    checks = max(1, int(timeout / PROCESS_POLL_SECONDS) + 2)
    for _ in range(checks):
        if failure_event.is_set():
            returncode, termination_error = _terminate_process_tree(process)
            return returncode, False, termination_error
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            break
        try:
            return process.wait(timeout=min(PROCESS_POLL_SECONDS, remaining)), False, ""
        except subprocess.TimeoutExpired:
            continue
    returncode, termination_error = _terminate_process_tree(process)
    return returncode, True, termination_error


def _terminate_process_tree(process: subprocess.Popen[bytes]) -> tuple[int, str]:
    """Terminate one bounded process tree and reap its direct child."""
    if os.name == "posix":
        failures = _terminate_posix_process_group(process)
    elif os.name == "nt":
        failures = _terminate_windows_process_tree(process)
    else:
        failures = _terminate_direct_process(process)
    try:
        return process.wait(timeout=PROCESS_KILL_WAIT_SECONDS), "; ".join(failures)
    except subprocess.TimeoutExpired:
        failures.append("direct child remained alive after forced termination")
        return -1, "; ".join(failures)


def _terminate_posix_process_group(process: subprocess.Popen[bytes]) -> list[str]:
    """Signal the isolated POSIX process group with bounded TERM then KILL."""
    failures: list[str] = []
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        pass
    except OSError as error:
        failures.append(f"terminate process group: {error}")
    time.sleep(PROCESS_TERMINATE_GRACE_SECONDS)
    try:
        os.killpg(process.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
    except OSError as error:
        failures.append(f"kill process group: {error}")
    return failures


def _terminate_windows_process_tree(process: subprocess.Popen[bytes]) -> list[str]:
    """Use taskkill's tree mode, then retain a direct-child fallback."""
    failures: list[str] = []
    try:
        result = subprocess.run(  # noqa: S603 - fixed Windows tree-kill utility.
            ("taskkill", "/PID", str(process.pid), "/T", "/F"),  # noqa: S607 - Windows provides taskkill on PATH.
            check=False,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            timeout=PROCESS_TASKKILL_SECONDS,
        )
        if result.returncode != 0 and process.poll() is None:
            failures.append(f"taskkill exited {result.returncode}")
    except (OSError, subprocess.SubprocessError) as error:
        failures.append(f"taskkill failed: {error}")
    failures.extend(_terminate_direct_process(process))
    return failures


def _terminate_direct_process(process: subprocess.Popen[bytes]) -> list[str]:
    """Bounded fallback for hosts without process-tree primitives."""
    if process.poll() is not None:
        return []
    try:
        process.terminate()
        process.wait(timeout=PROCESS_TERMINATE_GRACE_SECONDS)
        return []
    except subprocess.TimeoutExpired:
        pass
    except OSError as error:
        return [f"terminate direct child: {error}"]
    try:
        process.kill()
    except OSError as error:
        return [f"kill direct child: {error}"]
    return []


def _finish_process_readers(
    process: subprocess.Popen[bytes],
    threads: list[threading.Thread],
    streams: tuple[BinaryIO | None, BinaryIO | None],
    read_errors: list[OSError | None],
) -> str:
    """Bound reader joins and force-close inherited pipes after child exit."""
    if _join_threads(threads, PROCESS_PIPE_JOIN_SECONDS):
        return ""
    _, termination_error = _terminate_process_tree(process)
    _close_process_streams(streams, read_errors)
    if _join_threads(threads, PROCESS_PIPE_JOIN_SECONDS):
        suffix = f"; {termination_error}" if termination_error else ""
        return "command output pipes remained open after direct child exit" + suffix
    suffix = f": {termination_error}" if termination_error else ""
    return "command output reader threads did not stop after forced pipe close" + suffix


def _join_threads(threads: list[threading.Thread], timeout: float) -> bool:
    """Join all readers against one scalar deadline."""
    deadline = time.monotonic() + timeout
    for thread in threads:
        thread.join(timeout=max(deadline - time.monotonic(), 0))
    return all(not thread.is_alive() for thread in threads)


def _close_process_streams(
    streams: tuple[BinaryIO | None, BinaryIO | None],
    read_errors: list[OSError | None],
) -> None:
    """Close both pipe handles while retaining the first read/close failure."""
    for index, stream in enumerate(streams):
        if stream is None:
            continue
        try:
            stream.close()
        except OSError as error:
            read_errors[index] = read_errors[index] or error


def _raise_process_output_error(
    argv: tuple[str, ...],
    timeout: int,
    limit: int,
    timed_out: bool,
    read_errors: list[OSError | None],
    overflow: list[bool],
    termination_error: str,
    pipe_error: str,
) -> None:
    """Convert bounded-capture failures into stable portability errors."""
    failure = ""
    if timed_out:
        failure = f"command timed out after {timeout}s: {argv!r}"
    error = next((item for item in read_errors if item is not None), None)
    if not failure and error is not None:
        failure = f"command output read failed {argv!r}: {error}"
    if not failure and any(overflow):
        failure = f"command output exceeds {limit} bytes: {argv!r}"
    if not failure and pipe_error:
        failure = f"{pipe_error}: {argv!r}"
    cleanup_errors = "; ".join(item for item in (termination_error, pipe_error) if item)
    if cleanup_errors and failure:
        failure += f"; process cleanup: {cleanup_errors}"
    elif cleanup_errors:
        failure = f"process cleanup failed {argv!r}: {cleanup_errors}"
    if failure:
        raise PortabilityError(failure)


def repository_environment() -> dict[str, str]:
    """Return a read-only module environment without replacing caller flags."""
    environment = dict(os.environ)
    goflags = environment.get("GOFLAGS", "")
    if re.search(r"(?:^|\s)-mod(?:=|\s)", goflags) is None:
        environment["GOFLAGS"] = f"{goflags} -mod=readonly".strip()
    return environment


def validate_manifest_path(root: Path, raw_path: str) -> Path:
    """Resolve one slash-form Git path and reject ambiguous or escaping inputs."""
    if not raw_path or "\\" in raw_path or ":" in raw_path or len(raw_path) > 4096:
        raise PortabilityError(f"unsupported Go module path: {raw_path!r}")
    relative = PurePosixPath(raw_path)
    if (
        relative.is_absolute()
        or relative.name != "go.mod"
        or ".." in relative.parts
        or len(relative.parts) > MAX_PATH_COMPONENTS
    ):
        raise PortabilityError(f"unsupported Go module path: {raw_path!r}")
    current = root
    for component in relative.parts:
        current /= component
        if current.is_symlink():
            raise PortabilityError(f"Go module path contains a symlink: {raw_path}")
    if not current.is_file():
        raise PortabilityError(f"discovered module file is absent: {raw_path}")
    module = current.parent.resolve(strict=True)
    if not module.is_relative_to(root):
        raise PortabilityError(f"Go module escapes repository root: {raw_path}")
    return module


def decode_manifest_output(output: bytes, label: str) -> tuple[str, ...]:
    """Decode one bounded NUL-delimited Git manifest list."""
    if len(output) > MAX_MANIFEST_OUTPUT_BYTES:
        raise PortabilityError(f"{label} output exceeds the 1 MiB bound")
    try:
        paths = tuple(item.decode("utf-8") for item in output.split(b"\0") if item)
    except UnicodeDecodeError as error:
        raise PortabilityError(f"{label} path is not valid UTF-8") from error
    if len(set(paths)) != len(paths):
        raise PortabilityError(f"{label} returned duplicate paths")
    return paths


def discover_modules(root: Path, runner: ProcessRunner) -> tuple[Path, ...]:
    """Discover bounded tracked and non-ignored Go modules through Git."""
    environment = repository_environment()
    output = runner.output(
        (
            "git",
            "-C",
            str(root),
            "ls-files",
            "-z",
            "--cached",
            "--others",
            "--exclude-standard",
            "--",
            "go.mod",
            ":(glob)**/go.mod",
        ),
        cwd=root,
        environment=environment,
        timeout=DISCOVERY_TIMEOUT_SECONDS,
    )
    deleted_output = runner.output(
        (
            "git",
            "-C",
            str(root),
            "ls-files",
            "-z",
            "--deleted",
            "--",
            "go.mod",
            ":(glob)**/go.mod",
        ),
        cwd=root,
        environment=environment,
        timeout=DISCOVERY_TIMEOUT_SECONDS,
    )
    discovered_paths = decode_manifest_output(output, "Go module discovery")
    deleted_paths = set(decode_manifest_output(deleted_output, "deleted Go module discovery"))
    if not deleted_paths.issubset(discovered_paths):
        raise PortabilityError("deleted Go module discovery returned an unknown path")
    raw_paths = [path for path in discovered_paths if path not in deleted_paths]
    if not 1 <= len(raw_paths) <= MAX_MODULES:
        raise PortabilityError(
            f"discovered {len(raw_paths)} Go modules; expected 1..{MAX_MODULES}"
        )
    if "go.mod" not in raw_paths:
        raise PortabilityError("discovered module set lacks root go.mod")
    modules = [validate_manifest_path(root, path) for path in raw_paths]
    return tuple(sorted(modules, key=lambda path: (path != root, path.as_posix())))


def list_packages(
    module: Path,
    runner: ProcessRunner,
    environment: dict[str, str],
) -> int:
    """Count packages so a successful no-work phase cannot pass."""
    output = runner.output(
        ("go", "list", "./..."),
        cwd=module,
        environment=environment,
        timeout=LIST_TIMEOUT_SECONDS,
    )
    try:
        packages = tuple(line for line in output.decode("utf-8").splitlines() if line)
    except UnicodeDecodeError as error:
        raise PortabilityError(f"go list returned non-UTF-8 output in {module}") from error
    if not 1 <= len(packages) <= MAX_PACKAGES_PER_MODULE:
        raise PortabilityError(
            f"discovered {len(packages)} packages in {module}; "
            f"expected 1..{MAX_PACKAGES_PER_MODULE}"
        )
    if len(set(packages)) != len(packages):
        raise PortabilityError(f"go list returned duplicate packages in {module}")
    return len(packages)


def module_name(root: Path, module: Path) -> str:
    """Render a stable slash-form module path for logs on every host."""
    relative = module.relative_to(root)
    return "." if relative == Path() else relative.as_posix()


def verify_repository(root: Path, runner: ProcessRunner | None = None) -> Summary:
    """Run every portability phase for every discovered module."""
    resolved_root = root.resolve(strict=True)
    process_runner = runner or ProcessRunner()
    environment = repository_environment()
    modules = discover_modules(resolved_root, process_runner)
    package_total = 0
    phase_total = 0
    for module in modules:
        name = module_name(resolved_root, module)
        package_count = list_packages(module, process_runner, environment)
        package_total += package_count
        print(f"==> {name}: {package_count} package(s)", flush=True)
        for phase, argv in PHASES:
            print(f"    {phase}", flush=True)
            process_runner.run(
                argv,
                cwd=module,
                environment=environment,
                timeout=PHASE_TIMEOUT_SECONDS,
            )
            phase_total += 1
    expected_phases = len(modules) * len(PHASES)
    if package_total == 0 or phase_total != expected_phases:
        raise PortabilityError(
            f"portability work collapsed: modules={len(modules)} packages={package_total} "
            f"phases={phase_total}/{expected_phases}"
        )
    return Summary(modules=len(modules), packages=package_total, phases=phase_total)


def main(argv: list[str] | None = None) -> int:
    """Run the command-line gate."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--root",
        type=Path,
        default=Path(__file__).resolve().parents[2],
        help="repository root (default: inferred from this script)",
    )
    arguments = parser.parse_args(argv)
    try:
        summary = verify_repository(arguments.root)
    except (OSError, PortabilityError) as error:
        print(f"portability: {error}", file=sys.stderr)
        return 1
    print(
        f"portability: {summary.modules} module(s), {summary.packages} package(s), "
        f"{summary.phases} phase(s) passed on {sys.platform}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
