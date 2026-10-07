#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Positive, negative, and boundary tests for the HISS-21 portability driver."""

from __future__ import annotations

import contextlib
import gzip
import hashlib
import http.client
import io
import os
import sys
import tempfile
import time
import unittest
from pathlib import Path
from unittest import mock

import pact_ffi
import portability

ROOT = Path(__file__).resolve().parents[2]


class FakeRunner:
    """Return deterministic Git and Go output while recording phase execution."""

    def __init__(
        self,
        manifests: tuple[str, ...],
        *,
        deleted_manifests: tuple[str, ...] = (),
        packages: bytes = b"example.test/package\n",
        fail_phase: str = "",
    ) -> None:
        self.manifests = manifests
        self.deleted_manifests = deleted_manifests
        self.packages = packages
        self.fail_phase = fail_phase
        self.commands: list[tuple[Path, tuple[str, ...]]] = []

    def output(
        self,
        argv: tuple[str, ...],
        *,
        cwd: Path,
        environment: dict[str, str],
        timeout: int,
    ) -> bytes:
        del environment, timeout
        self.commands.append((cwd, argv))
        if argv[0] == "git":
            manifests = self.deleted_manifests if "--deleted" in argv else self.manifests
            return b"\0".join(path.encode() for path in manifests) + b"\0"
        if argv == ("go", "list", "./..."):
            return self.packages
        raise AssertionError(f"unexpected output command: {argv!r}")

    def run(
        self,
        argv: tuple[str, ...],
        *,
        cwd: Path,
        environment: dict[str, str],
        timeout: int,
    ) -> None:
        del environment, timeout
        self.commands.append((cwd, argv))
        if self.fail_phase and argv[1] == self.fail_phase:
            raise portability.PortabilityError(f"injected {self.fail_phase} failure")


def create_repository(root: Path, manifests: tuple[str, ...]) -> None:
    """Create only the manifests the discovery fixture names."""
    for manifest in manifests:
        path = root.joinpath(*manifest.split("/"))
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("module example.test/fixture\n\ngo 1.27\n", encoding="utf-8")


def verify_quiet(root: Path, runner: FakeRunner) -> portability.Summary:
    """Run a fixture without flooding policy output with bounded fake phases."""
    with contextlib.redirect_stdout(io.StringIO()):
        return portability.verify_repository(root, runner)


class PortabilityDriverTest(unittest.TestCase):
    def test_process_output_bound_accepts_exact_and_rejects_overflow(self) -> None:
        runner = portability.ProcessRunner()
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            exact = runner.output(
                (sys.executable, "-c", "import sys; sys.stdout.buffer.write(b'x' * 4)"),
                cwd=root,
                environment=dict(os.environ),
                timeout=10,
                max_output_bytes=4,
            )
            self.assertEqual(exact, b"xxxx")
            with self.assertRaisesRegex(
                portability.PortabilityError, "output exceeds 4 bytes"
            ):
                runner.output(
                    (
                        sys.executable,
                        "-c",
                        "import sys,time; sys.stdout.buffer.write(b'x' * 5); "
                        "sys.stdout.flush(); time.sleep(10)",
                    ),
                    cwd=root,
                    environment=dict(os.environ),
                    timeout=10,
                    max_output_bytes=4,
                )

    def test_process_output_overflow_interrupts_delayed_child(self) -> None:
        runner = portability.ProcessRunner()
        started = time.monotonic()
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaisesRegex(
                portability.PortabilityError, "output exceeds 4 bytes"
            ):
                runner.output(
                    (
                        sys.executable,
                        "-c",
                        "import sys,time; sys.stdout.buffer.write(b'x' * 5); "
                        "sys.stdout.flush(); time.sleep(10)",
                    ),
                    cwd=Path(directory),
                    environment=dict(os.environ),
                    timeout=8,
                    max_output_bytes=4,
                )
        self.assertLess(time.monotonic() - started, 3)

    def test_process_output_bounds_pipe_inheriting_descendant(self) -> None:
        runner = portability.ProcessRunner()
        grandchild = "import time; time.sleep(10)"
        parent = (
            "import subprocess,sys; "
            f"subprocess.Popen([sys.executable, '-c', {grandchild!r}]); "
        )
        started = time.monotonic()
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaisesRegex(
                portability.PortabilityError, "output pipes remained open"
            ):
                runner.output(
                    (sys.executable, "-c", parent),
                    cwd=Path(directory),
                    environment=dict(os.environ),
                    timeout=8,
                    max_output_bytes=4,
                )
        self.assertLess(time.monotonic() - started, 4)

    def test_positive_every_module_runs_all_three_phases(self) -> None:
        manifests = ("go.mod", "nested/tool/go.mod")
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            create_repository(root, manifests)
            runner = FakeRunner(manifests)
            summary = verify_quiet(root, runner)
        self.assertEqual(summary, portability.Summary(modules=2, packages=2, phases=6))
        phase_commands = [argv for _, argv in runner.commands if argv[0] == "go" and argv[1] != "list"]
        self.assertEqual(phase_commands, [argv for _ in manifests for _, argv in portability.PHASES])

    def test_negative_empty_discovery_fails_closed(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            runner = FakeRunner(())
            with self.assertRaisesRegex(portability.PortabilityError, "expected 1..64"):
                verify_quiet(Path(directory), runner)

    def test_positive_deleted_manifest_is_ignored(self) -> None:
        manifests = ("go.mod", "retired/go.mod")
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            create_repository(root, ("go.mod",))
            runner = FakeRunner(manifests, deleted_manifests=("retired/go.mod",))
            summary = verify_quiet(root, runner)
        self.assertEqual(summary, portability.Summary(modules=1, packages=1, phases=3))

    def test_negative_unknown_deleted_manifest_fails_closed(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            create_repository(root, ("go.mod",))
            runner = FakeRunner(("go.mod",), deleted_manifests=("unknown/go.mod",))
            with self.assertRaisesRegex(portability.PortabilityError, "unknown path"):
                verify_quiet(root, runner)

    def test_boundary_module_limit_accepts_64_and_rejects_65(self) -> None:
        accepted = ("go.mod", *(f"module-{index:02d}/go.mod" for index in range(63)))
        rejected = (*accepted, "module-63/go.mod")
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            create_repository(root, rejected)
            summary = verify_quiet(root, FakeRunner(accepted))
            with self.assertRaisesRegex(portability.PortabilityError, "expected 1..64"):
                verify_quiet(root, FakeRunner(rejected))
        self.assertEqual(summary.modules, portability.MAX_MODULES)

    def test_negative_root_module_is_mandatory(self) -> None:
        manifests = ("nested/go.mod",)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            create_repository(root, manifests)
            with self.assertRaisesRegex(portability.PortabilityError, "lacks root go.mod"):
                verify_quiet(root, FakeRunner(manifests))

    def test_negative_zero_packages_cannot_report_green(self) -> None:
        manifests = ("go.mod",)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            create_repository(root, manifests)
            runner = FakeRunner(manifests, packages=b"")
            with self.assertRaisesRegex(portability.PortabilityError, "expected 1..512"):
                verify_quiet(root, runner)
        phase_commands = [argv for _, argv in runner.commands if argv[0] == "go" and argv[1] != "list"]
        self.assertEqual(phase_commands, [])

    def test_negative_phase_failure_is_not_hidden(self) -> None:
        manifests = ("go.mod",)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            create_repository(root, manifests)
            with self.assertRaisesRegex(portability.PortabilityError, "injected vet failure"):
                verify_quiet(root, FakeRunner(manifests, fail_phase="vet"))

    def test_negative_ambiguous_windows_paths_are_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            create_repository(root, ("go.mod",))
            for invalid_path in ("nested\\tool\\go.mod", "C:/tool/go.mod"):
                with self.subTest(path=invalid_path):
                    manifests = ("go.mod", invalid_path)
                    with self.assertRaisesRegex(
                        portability.PortabilityError, "unsupported Go module path"
                    ):
                        verify_quiet(root, FakeRunner(manifests))

    def test_workflow_uses_pinned_three_platform_matrix(self) -> None:
        workflow = (ROOT / ".github" / "workflows" / "portability.yml").read_text(
            encoding="utf-8"
        )
        required = (
            "fail-fast: false",
            "runner: ubuntu-latest",
            "runner: macos-latest",
            "runner: windows-latest",
            "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1",
            "actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e",
            "actions/setup-python@5fda3b95a4ea91299a34e894583c3862153e4b97",
            'pact_dir="${RUNNER_TEMP}/pact-ffi"',
            'install -d -m 0700 "$pact_dir"',
            'python -B scripts/ci/pact_ffi.py --lib-dir "$pact_dir"',
            'PACT_GO_LIB_DOWNLOAD_PATH=$pact_dir',
            "python -B scripts/ci/pact_ffi.py --lib-dir $pactDir",
            "CGO_LDFLAGS=-L$pactDir",
            "python -B scripts/ci/portability_test.py",
            "python -B scripts/ci/portability.py",
            "first hosted green",
        )
        for fragment in required:
            with self.subTest(fragment=fragment):
                self.assertIn(fragment, workflow)
        self.assertNotIn("continue-on-error:", workflow)
        self.assertNotIn("pact-go/v2 install", workflow)
        self.assertNotIn("--lib-dir /tmp", workflow)
        self.assertLess(
            workflow.index("Verify portability driver policy"),
            workflow.index("Install verified Pact FFI"),
        )


class PactFFIInstallerTests(unittest.TestCase):
    def test_asset_lock_covers_every_hosted_runner_architecture(self) -> None:
        expected = {
            (system_name, architecture)
            for system_name in ("Linux", "Darwin", "Windows")
            for architecture in ("x86_64", "aarch64")
        }
        self.assertEqual(set(pact_ffi.ASSETS), expected)
        for asset in pact_ffi.ASSETS.values():
            self.assertRegex(asset.sha256, r"^[a-f0-9]{64}$")

    def test_positive_verified_archive_installs_expected_library(self) -> None:
        payload = b"verified pact ffi"
        compressed = gzip.compress(payload)
        asset = pact_ffi.Asset(
            filename="fixture.so.gz",
            sha256=hashlib.sha256(compressed).hexdigest(),
            library="libpact_ffi.so",
        )
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            archive = root / asset.filename
            archive.write_bytes(compressed)
            installed = pact_ffi.extract_archive(archive, root, asset)
            self.assertEqual(installed.read_bytes(), payload)

    def test_negative_digest_mismatch_leaves_no_library(self) -> None:
        compressed = gzip.compress(b"attacker-controlled bytes")
        asset = pact_ffi.Asset(
            filename="fixture.so.gz",
            sha256="0" * 64,
            library="libpact_ffi.so",
        )
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            archive = root / asset.filename
            archive.write_bytes(compressed)
            with self.assertRaisesRegex(pact_ffi.InstallError, "digest mismatch"):
                pact_ffi.extract_archive(archive, root, asset)
            self.assertFalse((root / asset.library).exists())

    def test_truncated_download_is_wrapped_and_removed(self) -> None:
        class TruncatedResponse:
            def __enter__(self) -> TruncatedResponse:
                return self

            def __exit__(self, *_args: object) -> None:
                return None

            def read(self, _size: int) -> bytes:
                raise http.client.IncompleteRead(b"partial", 99)

        asset = pact_ffi.Asset(
            filename="fixture.so.gz",
            sha256="0" * 64,
            library="libpact_ffi.so",
        )
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            with (
                mock.patch.object(
                    pact_ffi.urllib.request,
                    "urlopen",
                    return_value=TruncatedResponse(),
                ),
                self.assertRaisesRegex(pact_ffi.InstallError, "download failed"),
            ):
                pact_ffi.download_archive(asset, root)
            self.assertEqual(list(root.glob(".pact-ffi-*")), [])

    def test_close_failure_still_removes_temporary_archive(self) -> None:
        real_factory = tempfile.NamedTemporaryFile

        class CloseFailingTemporary:
            def __init__(self, *args: object, **kwargs: object) -> None:
                self.file = real_factory(*args, **kwargs)
                self.name = self.file.name

            def __enter__(self) -> CloseFailingTemporary:
                return self

            def __exit__(self, *_args: object) -> None:
                self.close()

            def write(self, data: bytes) -> int:
                return self.file.write(data)

            def close(self) -> None:
                self.file.close()
                raise OSError("injected close failure")

        asset = pact_ffi.Asset(
            filename="fixture.so.gz",
            sha256=hashlib.sha256(b"payload").hexdigest(),
            library="libpact_ffi.so",
        )
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            with (
                mock.patch.object(
                    pact_ffi.tempfile,
                    "NamedTemporaryFile",
                    side_effect=CloseFailingTemporary,
                ),
                mock.patch.object(
                    pact_ffi.urllib.request,
                    "urlopen",
                    return_value=io.BytesIO(b"payload"),
                ),
                self.assertRaisesRegex(pact_ffi.InstallError, "injected close failure"),
            ):
                pact_ffi.download_archive(asset, root)
            self.assertEqual(list(root.glob(".pact-ffi-*")), [])

    def test_boundary_decompressed_size_is_bounded(self) -> None:
        for payload, should_pass in ((b"1234", True), (b"12345", False)):
            with self.subTest(size=len(payload)):
                compressed = gzip.compress(payload)
                asset = pact_ffi.Asset(
                    filename="fixture.so.gz",
                    sha256=hashlib.sha256(compressed).hexdigest(),
                    library="libpact_ffi.so",
                )
                with tempfile.TemporaryDirectory() as directory:
                    root = Path(directory)
                    archive = root / asset.filename
                    archive.write_bytes(compressed)
                    if should_pass:
                        pact_ffi.extract_archive(
                            archive, root, asset, max_library_bytes=4
                        )
                    else:
                        with self.assertRaisesRegex(
                            pact_ffi.InstallError, "decompressed size"
                        ):
                            pact_ffi.extract_archive(
                                archive, root, asset, max_library_bytes=4
                            )

    def test_pact_go_version_pin_must_match_installer_contract(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            module = Path(directory) / "go.mod"
            module.write_text(
                "module fixture\n\n"
                "require github.com/pact-foundation/pact-go/v2 v9.9.9\n",
                encoding="utf-8",
            )
            with self.assertRaisesRegex(pact_ffi.InstallError, "expected v2.8.0"):
                pact_ffi.verify_pact_go_pin(module)

    def test_positive_pact_go_version_pin_matches_installer_contract(self) -> None:
        pact_ffi.verify_pact_go_pin(ROOT / "testutil" / "pact" / "go.mod")


if __name__ == "__main__":
    unittest.main(verbosity=2)
