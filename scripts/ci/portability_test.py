#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Positive, negative, and boundary tests for the HISS-21 portability driver."""

from __future__ import annotations

import ast
import contextlib
import errno
import gzip
import hashlib
import http.client
import io
import os
import re
import shutil
import subprocess  # Runs only resolved make dry runs of fixed repository goals. # nosec B404
import sys
import tempfile
import time
import urllib.error
from pathlib import Path
from unittest import TestCase, main, mock, skipUnless

import pact_ffi
import portability

ROOT = Path(__file__).resolve().parents[2]
UNSAFE_TMPDIR = "/tmp"  # noqa: S108 - fallback selection input, never written. # nosec B108


# Two pipe joins, the tree kill, its grace and the direct-child reap, plus 3 s for two interpreter starts (Windows took 4.97 s).
TEARDOWN_BUDGET_SECONDS = (
    2 * portability.PROCESS_PIPE_JOIN_SECONDS
    + portability.PROCESS_TASKKILL_SECONDS
    + portability.PROCESS_TERMINATE_GRACE_SECONDS
    + portability.PROCESS_KILL_WAIT_SECONDS
    + 3
)


def _is_pure_number(node: ast.expr) -> bool:
    """Report whether an expression is built from numeric literals only."""
    leaves = [n for n in ast.walk(node) if isinstance(n, (ast.Name, ast.Attribute, ast.Call, ast.Constant))]
    return all(isinstance(n, ast.Constant) and isinstance(n.value, (int, float)) for n in leaves)


def literal_wall_clock_bounds(source: str) -> list[int]:
    """Return lines whose assert compares a time.monotonic() expression with a pure number."""
    lines = []
    for node in ast.walk(ast.parse(source)):
        if not (isinstance(node, ast.Call) and isinstance(node.func, ast.Attribute)):
            continue
        if not node.func.attr.startswith("assert"):
            continue
        timed = any("monotonic" in ast.unparse(arg) for arg in node.args)
        if timed and any(_is_pure_number(arg) for arg in node.args):
            lines.append(node.lineno)
    return lines


def pact_modifier_violations(makefile: str) -> list[str]:
    """Return non-Windows Pact assignments with combined modifiers or missing origin-guarded exports."""
    before_windows, windows = makefile.split("ifeq ($(OS),Windows_NT)\n", 1)
    lines = (before_windows + windows.split("\nelse\n", 1)[1]).splitlines()
    offending = []
    prefix = "$(PACT_FFI_TARGETS): "
    for index, line in enumerate(lines):
        if not line.startswith(prefix) or ":=" not in line:
            continue
        modifiers = line.removeprefix(prefix).split(":=", 1)[0].split()
        if sum(token in {"override", "export"} for token in modifiers) > 1:
            offending.append(line)
        elif "override" in modifiers:
            name = modifiers[-1]
            export = line.replace(": override ", ": export ", 1)
            expected = [f"ifeq ($(origin {name}),command line)", f"export {name}", "else", export, "endif"]
            if lines[index + 1 : index + 6] != expected:
                offending.append(line)
    return offending


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


class PortabilityDriverTest(TestCase):
    def setUp(self) -> None:
        # Fixture roots are not git repositories; a pull request job's base sha must not scope them.
        patcher = mock.patch.dict(os.environ, {"CI_SCOPE_BASE": ""})
        patcher.start()
        self.addCleanup(patcher.stop)

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
                        "sys.stdout.flush(); time.sleep(60)",
                    ),
                    cwd=Path(directory),
                    environment=dict(os.environ),
                    timeout=30,
                    max_output_bytes=4,
                )
        self.assertLess(time.monotonic() - started, TEARDOWN_BUDGET_SECONDS)

    def test_wall_clock_bounds_derive_from_runner_constants(self) -> None:
        # A literal bound drifts below the runner's own teardown constants and flakes on slow hosts.
        source = Path(__file__).read_text(encoding="utf-8")
        self.assertEqual(literal_wall_clock_bounds(source), [])
        planted = (
            "self.assertLess(time.monotonic() - started, 3)",
            "self.assertLess((time.monotonic() - started), 3)",
            "self.assertLessEqual(time.monotonic() - started, 2 + 1.5)",
            "self.assertGreater(3, time.monotonic() - started)",
        )
        for line in planted:
            with self.subTest(line=line):
                self.assertEqual(literal_wall_clock_bounds(line), [1])
        derived = "self.assertLess(time.monotonic() - started, TEARDOWN_BUDGET_SECONDS)"
        self.assertEqual(literal_wall_clock_bounds(derived), [])

    def test_process_output_bounds_pipe_inheriting_descendant(self) -> None:
        runner = portability.ProcessRunner()
        grandchild = "import time; time.sleep(60)"
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
                    timeout=30,
                    max_output_bytes=4,
                )
        self.assertLess(time.monotonic() - started, TEARDOWN_BUDGET_SECONDS)

    @skipUnless(os.name == "posix", "process groups are POSIX")
    def test_boundary_process_group_signal_refuses_unbounded_targets(self) -> None:
        refused = (0, 1, -5, True, os.getpgrp(), mock.MagicMock(), None)
        with (
            mock.patch.object(portability.os, "killpg") as killpg,
            mock.patch.object(portability.time, "sleep"),
        ):
            for pid in refused:
                with self.subTest(pid=pid):
                    failures = portability._terminate_posix_process_group(mock.Mock(pid=pid))
                    self.assertEqual(len(failures), 1)
            killpg.assert_not_called()
            # pid 2 is never signalled for real: os.killpg is the mock above.
            self.assertEqual(portability._terminate_posix_process_group(mock.Mock(pid=2)), [])
        self.assertEqual([call.args[0] for call in killpg.call_args_list], [2, 2])

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
        """Pin the portability matrix, its actions and the cached Pact FFI install path."""
        workflow = (ROOT / ".github" / "workflows" / "portability.yml").read_text(
            encoding="utf-8"
        )
        required = (
            "fail-fast: false",
            "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1",
            "uses: ./.github/actions/setup-go",
            "actions/setup-python@6d4fd22b20465ccc0f7354476588e8df7c9ca0e0",
            'pact_dir="${RUNNER_TEMP}/pact-ffi"',
            'install -d -m 0700 "$pact_dir"',
            'python -B scripts/ci/pact_ffi.py --cache-dir .workingdir/cache/pact-ffi --lib-dir "$pact_dir"',
            'PACT_GO_LIB_DOWNLOAD_PATH=$pact_dir',
            'CGO_LDFLAGS=-L$pact_dir -Wl,-rpath,$pact_dir',
            "$pactDir = Join-Path $env:RUNNER_TEMP 'pact-ffi'",
            "python -B scripts/ci/pact_ffi.py --cache-dir .workingdir/cache/pact-ffi --lib-dir $pactDir",
            "CGO_LDFLAGS=-L$pactDir",
            "python -B scripts/ci/portability_test.py",
            "run: bash scripts/ci/release-depth-evidence_test.sh",
            "uses: ./.github/actions/install-cgo-libs",
            "python -B scripts/ci/portability.py",
            "first hosted green",
        )
        for fragment in required:
            with self.subTest(fragment=fragment):
                self.assertIn(fragment, workflow)
        # Name-runner pairings: a renamed matrix entry stops emitting the required context.
        pairs = (
            ("Linux", "ubuntu-26.04"),
            ("Linux arm64", "ubuntu-26.04-arm"),
            ("macOS", "macos-26"),
            ("macOS Intel", "macos-15-intel"),
            ("Windows", "windows-2025"),
        )
        for name, runner in pairs:
            with self.subTest(name=name, runner=runner):
                self.assertRegex(
                    workflow,
                    rf"(?m)^ +- name: {re.escape(name)}\n +runner: {re.escape(runner)}$",
                )
        self.assertIn("continue-on-error: true", workflow)
        self.assertRegex(
            workflow, r"(?m)^ +name: Platform Neutrality \(Windows arm64\)"
        )
        self.assertRegex(workflow, r"(?m)^ +runs-on: windows-11-arm$")
        self.assertIn("steps: *portability-steps", workflow)
        self.assertIn("&portability-steps", workflow)
        # main requires the matrix contexts; without this trigger the merge queue waits for them forever.
        self.assertRegex(workflow, r"(?m)^  merge_group:$")
        # A floating label moves to the next system image without a reviewed change.
        self.assertNotRegex(workflow, r"runner:\s*\S*-latest")
        self.assertNotIn("pact-go/v2 install", workflow)
        self.assertNotIn("--lib-dir /tmp", workflow)
        self.assertLess(
            workflow.index("Verify portability driver policy"),
            workflow.index("Install verified Pact FFI"),
        )

    def test_workflow_install_steps_use_runner_temp(self) -> None:
        """Require every Unix install step to use RUNNER_TEMP and Windows to use env:RUNNER_TEMP."""
        workflows = (
            ROOT / ".github" / "workflows" / "ci.yml",
            ROOT / ".github" / "workflows" / "portability.yml",
        )
        install_steps = 0
        for path in workflows:
            text = path.read_text(encoding="utf-8")
            steps = re.split(r"(?m)^ {6}- ", text)
            for step in steps:
                if "scripts/ci/pact_ffi.py" not in step or "--lib-dir" not in step:
                    continue
                install_steps += 1
                if "shell: pwsh" in step:
                    self.assertIn("Join-Path $env:RUNNER_TEMP 'pact-ffi'", step)
                else:
                    self.assertIn('pact_dir="${RUNNER_TEMP}/pact-ffi"', step)
        self.assertEqual(install_steps, 4)


class WindowsARM64ToolchainTests(TestCase):
    def setUp(self) -> None:
        self.workflow = (ROOT / ".github/workflows/portability.yml").read_text(encoding="utf-8")

    def _toolchain_steps(self, workflow: str) -> tuple[str, str, str]:
        names = (
            "Resolve llvm-mingw archive pin on Windows arm64",
            "Cache llvm-mingw archive on Windows arm64",
            "Install pinned llvm-mingw on Windows arm64",
        )
        steps = re.split(r"(?m)^ {6}- ", workflow)
        selected = []
        for name in names:
            matches = [step for step in steps if step.startswith(f"name: {name}\n")]
            self.assertEqual(len(matches), 1, f"missing or duplicate toolchain step: {name}")
            step = matches[0]
            self.assertIn("if: runner.os == 'Windows' && runner.arch == 'ARM64' && env.CI_GATED != 'skip'", step)
            selected.append(step)
        return selected[0], selected[1], selected[2]

    def _assert_toolchain_contract(self, workflow: str) -> None:
        resolve, cache, install = self._toolchain_steps(workflow)
        self.assertIn("set -euo pipefail", resolve)
        self.assertIn(". tools/tool-versions.env", resolve)
        self.assertIn('"$LLVM_MINGW_VERSION" =~ ^[0-9]{8}$', resolve)
        self.assertIn('"$LLVM_MINGW_UCRT_AARCH64_SHA256" =~ ^[0-9a-f]{64}$', resolve)
        self.assertIn('"$LLVM_MINGW_UCRT_AARCH64_SHA256" >> "$GITHUB_OUTPUT"', resolve)
        self.assertIn("actions/cache@55cc8345863c7cc4c66a329aec7e433d2d1c52a9 # v6.1.0", cache)
        self.assertIn("path: .workingdir/cache/llvm-mingw", cache)
        self.assertIn("key: llvm-mingw-${{ runner.os }}-${{ runner.arch }}-${{ steps.llvm-mingw-pin.outputs.sha256 }}", cache)
        self.assertIn("shell: pwsh", install)
        self.assertIn("timeout-minutes: 10", install)
        self.assertIn("$ErrorActionPreference = 'Stop'", install)
        self.assertIn("LLVM_MINGW_VERSION: ${{ steps.llvm-mingw-pin.outputs.version }}", install)
        self.assertIn("LLVM_MINGW_UCRT_AARCH64_SHA256: ${{ steps.llvm-mingw-pin.outputs.sha256 }}", install)
        self.assertIn('-ConnectionTimeoutSeconds 30 -OperationTimeoutSeconds 300', install)
        guard = "if ((Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant() -ne $env:LLVM_MINGW_UCRT_AARCH64_SHA256) {\n            throw 'llvm-mingw archive digest mismatch'\n          }"
        self.assertIn(guard, install)
        extraction = "Expand-Archive -LiteralPath $archive -DestinationPath $destination"
        self.assertLess(install.index(guard), install.index(extraction))
        self.assertLess(install.index(extraction), install.index("$env:GITHUB_PATH"))
        self.assertIn("$env:CC = 'aarch64-w64-mingw32-clang'", install)
        self.assertIn("$env:CXX = 'aarch64-w64-mingw32-clang++'", install)
        self.assertIn('"CC=$env:CC", "CXX=$env:CXX" | Out-File -FilePath $env:GITHUB_ENV', install)
        self.assertIn('& (Join-Path $bin "$env:CC.exe") --version', install)
        self.assertIn("if ($LASTEXITCODE -ne 0) { throw 'llvm-mingw compiler version check failed' }", install)
        self.assertLess(workflow.index(resolve), workflow.index(cache))
        self.assertLess(workflow.index(cache), workflow.index(install))
        self.assertLess(workflow.index(install), workflow.index("name: Install verified Pact FFI on Windows"))
        self.assertIn("    steps: &portability-steps", workflow)
        self.assertRegex(workflow, r"(?m)^    continue-on-error: true\n    steps: \*portability-steps$")

    def test_windows_arm64_toolchain_is_pinned_verified_and_scoped(self) -> None:
        self._assert_toolchain_contract(self.workflow)

    def test_windows_arm64_toolchain_rejects_missing_digest_check(self) -> None:
        marker = "if ((Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant() -ne $env:LLVM_MINGW_UCRT_AARCH64_SHA256) {"
        mutant = self.workflow.replace(marker, "if ($false) {", 1)
        self.assertNotEqual(mutant, self.workflow)
        with self.assertRaises(AssertionError):
            self._assert_toolchain_contract(mutant)

    def test_windows_arm64_toolchain_rejects_missing_arm64_gate(self) -> None:
        for name in ("Resolve llvm-mingw archive pin", "Cache llvm-mingw archive", "Install pinned llvm-mingw"):
            with self.subTest(step=name):
                marker = f"name: {name} on Windows arm64\n        if: runner.os == 'Windows' && runner.arch == 'ARM64' &&"
                mutant = self.workflow.replace(marker, marker.replace(" && runner.arch == 'ARM64'", ""), 1)
                self.assertNotEqual(mutant, self.workflow)
                with self.assertRaises(AssertionError):
                    self._assert_toolchain_contract(mutant)

    def test_windows_arm64_toolchain_rejects_extraction_before_verification(self) -> None:
        marker = "          Expand-Archive -LiteralPath $archive -DestinationPath $destination\n"
        mutant = self.workflow.replace(marker, "", 1).replace(
            "          if ((Get-FileHash", marker + "          if ((Get-FileHash", 1
        )
        self.assertNotEqual(mutant, self.workflow)
        with self.assertRaises(AssertionError):
            self._assert_toolchain_contract(mutant)


def retry_fixture() -> tuple[bytes, bytes, pact_ffi.Asset]:
    """Return a payload, its gzip archive and the asset that pins that archive."""
    payload = b"verified pact ffi"
    compressed = gzip.compress(payload)
    asset = pact_ffi.Asset(
        filename="fixture.so.gz",
        sha256=hashlib.sha256(compressed).hexdigest(),
        library="libpact_ffi.so",
    )
    return payload, compressed, asset


class PactFFIInstallerTests(TestCase):
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
        payload, compressed, asset = retry_fixture()
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

    def _cache_install(
        self,
        root: Path,
        asset: pact_ffi.Asset,
        payload: bytes,
        *,
        cache_dir: Path | None = None,
        library_dir: Path | None = None,
    ) -> Path:
        """Run install(cache_root=root) with cache_dir and library_dir on a Linux fixture asset."""
        cdir = cache_dir if cache_dir is not None else root / "cache"
        ldir = library_dir if library_dir is not None else root / "lib"
        with (
            mock.patch.object(pact_ffi.platform, "system", return_value="Linux"),
            mock.patch.object(pact_ffi, "select_asset", return_value=asset),
            mock.patch.object(
                pact_ffi.urllib.request, "urlopen", return_value=io.BytesIO(payload)
            ) as urlopen,
        ):
            library = pact_ffi.install(
                ROOT / "testutil/pact/go.mod",
                ldir,
                cache_dir=cdir,
                cache_root=root,
            )
        self.download_calls = urlopen.call_count
        return library

    def test_positive_cache_downloads_once_then_reuses_verified_archive(self) -> None:
        """Reuse verified archive in cache_dir and extract library into library_dir."""
        compressed = gzip.compress(b"verified pact ffi")
        asset = pact_ffi.Asset(
            filename="fixture.so.gz",
            sha256=hashlib.sha256(compressed).hexdigest(),
            library="libpact_ffi.so",
        )
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            cache_dir = root / "cache"
            library_dir = root / "lib"
            installed = self._cache_install(
                root, asset, compressed, cache_dir=cache_dir, library_dir=library_dir
            )
            self.assertEqual(installed.read_bytes(), b"verified pact ffi")
            self.assertEqual(installed.parent, library_dir)
            self.assertEqual(self.download_calls, 1)
            self.assertTrue(pact_ffi.cached_archive_path(asset, cache_dir).is_file())
            self.assertFalse(pact_ffi.cached_archive_path(asset, library_dir).exists())
            self.assertFalse((library_dir / asset.filename).exists())
            self._cache_install(
                root, asset, b"", cache_dir=cache_dir, library_dir=library_dir
            )
            self.assertEqual(self.download_calls, 0)

    def test_negative_planted_cached_archive_digest_mismatch_is_refused(self) -> None:
        """Reject cached archive whose digest does not match the pinned asset sha256."""
        good = gzip.compress(b"verified pact ffi")
        asset = pact_ffi.Asset(
            filename="fixture.so.gz",
            sha256=hashlib.sha256(good).hexdigest(),
            library="libpact_ffi.so",
        )
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            cache_dir = root / "cache"
            cache_dir.mkdir(parents=True, exist_ok=True)
            pact_ffi.cached_archive_path(asset, cache_dir).write_bytes(gzip.compress(b"planted bytes"))
            with self.assertRaisesRegex(pact_ffi.InstallError, "digest mismatch"):
                self._cache_install(root, asset, good, cache_dir=cache_dir)
            self.assertFalse((root / "lib" / asset.library).exists())

    def test_negative_cache_download_digest_mismatch_keeps_nothing(self) -> None:
        """Ensure no artifact is kept in cache_dir or library_dir on digest mismatch."""
        asset = pact_ffi.Asset(
            filename="fixture.so.gz",
            sha256="0" * 64,
            library="libpact_ffi.so",
        )
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            with self.assertRaisesRegex(pact_ffi.InstallError, "digest mismatch"):
                self._cache_install(root, asset, gzip.compress(b"attacker bytes"))
            self.assertEqual(list((root / "cache").iterdir()), [])
            self.assertFalse((root / "lib").exists())

    def test_negative_rejected_cache_exits_with_error_code_three(self) -> None:
        """Return EXIT_ERROR when cached archive verification fails in main."""
        good = gzip.compress(b"verified pact ffi")
        asset = pact_ffi.Asset("fixture.so.gz", hashlib.sha256(good).hexdigest(), "libpact_ffi.so")
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            cache_dir = root / "cache"
            cache_dir.mkdir(parents=True, exist_ok=True)
            pact_ffi.cached_archive_path(asset, cache_dir).write_bytes(gzip.compress(b"planted bytes"))
            argv = ["pact_ffi.py", "--cache-dir", str(cache_dir), "--lib-dir", str(root / "lib")]
            argv += ["--module", str(ROOT / "testutil/pact/go.mod")]
            with (
                mock.patch.object(pact_ffi.sys, "argv", argv),
                mock.patch.object(pact_ffi.Path, "cwd", return_value=root),
                mock.patch.object(pact_ffi.platform, "system", return_value="Linux"),
                mock.patch.object(pact_ffi, "select_asset", return_value=asset),
                contextlib.redirect_stderr(io.StringIO()),
            ):
                self.assertEqual(pact_ffi.main(), 3)

    def _retry_download(
        self,
        responses: list[object],
        *,
        directory: Path | None = None,
        asset: pact_ffi.Asset | None = None,
    ) -> tuple[int, list[float], str, Path | Exception]:
        """Execute download_archive with patched urlopen, sleep capture, and stderr capture."""
        target_asset = asset if asset is not None else retry_fixture()[2]
        if directory is None:
            directory = Path(self.enterContext(tempfile.TemporaryDirectory()))
        sleeps: list[float] = []
        stderr_buffer = io.StringIO()
        with (
            mock.patch.object(
                pact_ffi.urllib.request, "urlopen", side_effect=list(responses)
            ) as urlopen,
            mock.patch.object(pact_ffi, "SLEEP", side_effect=sleeps.append),
            contextlib.redirect_stderr(stderr_buffer),
        ):
            try:
                outcome: Path | Exception = pact_ffi.download_archive(
                    target_asset, directory
                )
            except Exception as error:
                outcome = error
            return urlopen.call_count, sleeps, stderr_buffer.getvalue(), outcome

    def test_positive_download_retry_succeeds_after_transient_timeouts(self) -> None:
        payload, compressed, asset = retry_fixture()
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            responses: list[object] = [
                TimeoutError("first timeout"),
                TimeoutError("second timeout"),
                io.BytesIO(compressed),
            ]
            calls, sleeps, _stderr, outcome = self._retry_download(
                responses, directory=root, asset=asset
            )
            self.assertIsInstance(outcome, Path)
            self.assertTrue(outcome.is_file())
            installed = pact_ffi.extract_archive(outcome, root, asset)
            self.assertEqual(installed.read_bytes(), payload)
            self.assertEqual(calls, 3)
            self.assertEqual(sleeps, [2.0, 4.0])

    def test_negative_download_retry_exhausted_raises_install_error(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            calls, sleeps, _stderr, outcome = self._retry_download(
                [TimeoutError("timed out")] * 3, directory=root
            )
            self.assertIsInstance(outcome, pact_ffi.InstallError)
            self.assertRegex(
                str(outcome),
                r"Pact FFI download failed after 3 attempts: timed out",
            )
            self.assertEqual(calls, 3)
            self.assertEqual(sleeps, [2.0, 4.0])
            self.assertEqual(list(root.iterdir()), [])

    def test_negative_download_digest_mismatch_fails_at_once_without_retry(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            calls, sleeps, _stderr, outcome = self._retry_download(
                [io.BytesIO(b"wrong bytes")], directory=root
            )
            self.assertIsInstance(outcome, pact_ffi.InstallError)
            self.assertRegex(str(outcome), "digest mismatch")
            self.assertEqual(calls, 1)
            self.assertEqual(len(sleeps), 0)
            self.assertEqual(list(root.iterdir()), [])

    def test_negative_download_http_404_fails_at_once_without_retry(self) -> None:
        http_error = urllib.error.HTTPError(
            url="https://example.com/asset",
            code=404,
            msg="Not Found",
            hdrs={},
            fp=None,
        )
        self.addCleanup(http_error.close)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            calls, sleeps, _stderr, outcome = self._retry_download(
                [http_error], directory=root
            )
            self.assertIsInstance(outcome, pact_ffi.InstallError)
            self.assertRegex(
                str(outcome),
                r"Pact FFI download failed: HTTP Error 404: Not Found",
            )
            self.assertEqual(calls, 1)
            self.assertEqual(len(sleeps), 0)
            self.assertEqual(list(root.iterdir()), [])

    def test_boundary_download_http_503_retries_and_succeeds(self) -> None:
        _, compressed, asset = retry_fixture()
        http_error = urllib.error.HTTPError(
            url="https://example.com/asset",
            code=503,
            msg="Service Unavailable",
            hdrs={},
            fp=None,
        )
        self.addCleanup(http_error.close)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            calls, sleeps, _stderr, outcome = self._retry_download(
                [http_error, io.BytesIO(compressed)], directory=root, asset=asset
            )
            self.assertIsInstance(outcome, Path)
            self.assertTrue(outcome.is_file())
            self.assertEqual(outcome.read_bytes(), compressed)
            self.assertEqual(calls, 2)
            self.assertEqual(sleeps, [2.0])

    def test_boundary_download_http_408_retries_and_succeeds(self) -> None:
        _, compressed, asset = retry_fixture()
        http_error = urllib.error.HTTPError(
            url="https://example.com/asset",
            code=408,
            msg="Request Timeout",
            hdrs={},
            fp=None,
        )
        self.addCleanup(http_error.close)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            calls, sleeps, _stderr, outcome = self._retry_download(
                [http_error, io.BytesIO(compressed)], directory=root, asset=asset
            )
            self.assertIsInstance(outcome, Path)
            self.assertTrue(outcome.is_file())
            self.assertEqual(calls, 2)
            self.assertEqual(sleeps, [2.0])

    def test_positive_download_connection_reset_retries_and_succeeds(self) -> None:
        _, compressed, asset = retry_fixture()
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            calls, sleeps, _stderr, outcome = self._retry_download(
                [ConnectionResetError("connection reset"), io.BytesIO(compressed)],
                directory=root,
                asset=asset,
            )
            self.assertIsInstance(outcome, Path)
            self.assertTrue(outcome.is_file())
            self.assertEqual(calls, 2)
            self.assertEqual(sleeps, [2.0])

    def test_negative_download_local_enospc_fails_at_once_without_retry(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            calls, sleeps, _stderr, outcome = self._retry_download(
                [OSError(errno.ENOSPC, "No space left on device")], directory=root
            )
            self.assertIsInstance(outcome, pact_ffi.InstallError)
            self.assertIn("No space left on device", str(outcome))
            self.assertEqual(calls, 1)
            self.assertEqual(len(sleeps), 0)
            self.assertEqual(list(root.iterdir()), [])

    def test_negative_download_named_temporary_file_permission_error_fails_at_once(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            with mock.patch.object(
                pact_ffi.tempfile,
                "NamedTemporaryFile",
                side_effect=PermissionError("permission denied"),
            ) as mock_tempfile:
                calls, sleeps, _stderr, outcome = self._retry_download([], directory=root)
            self.assertIsInstance(outcome, (pact_ffi.InstallError, OSError))
            self.assertEqual(mock_tempfile.call_count, 1)
            self.assertEqual(len(sleeps), 0)
            self.assertEqual(calls, 0)
            self.assertEqual(list(root.iterdir()), [])

    def test_positive_download_retry_stderr_reports_intermediate_attempts(self) -> None:
        _, compressed, asset = retry_fixture()
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            responses: list[object] = [
                TimeoutError("first timeout"),
                TimeoutError("second timeout"),
                io.BytesIO(compressed),
            ]
            calls, sleeps, stderr_text, outcome = self._retry_download(
                responses, directory=root, asset=asset
            )
            self.assertIsInstance(outcome, Path)
            lines = [line for line in stderr_text.splitlines() if line]
            self.assertEqual(len(lines), 2)
            self.assertTrue(lines[0].startswith("pact-ffi: download attempt 1/3 failed:"))
            self.assertTrue(lines[1].startswith("pact-ffi: download attempt 2/3 failed:"))
            self.assertEqual(calls, 3)
            self.assertEqual(sleeps, [2.0, 4.0])

    def test_negative_download_retry_exhausted_stderr_has_no_warning_after_last_attempt(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            calls, sleeps, stderr_text, outcome = self._retry_download(
                [TimeoutError("timed out")] * 3, directory=root
            )
            self.assertIsInstance(outcome, pact_ffi.InstallError)
            lines = [line for line in stderr_text.splitlines() if line]
            self.assertEqual(len(lines), 2)
            self.assertTrue(lines[0].startswith("pact-ffi: download attempt 1/3 failed:"))
            self.assertTrue(lines[1].startswith("pact-ffi: download attempt 2/3 failed:"))
            self.assertEqual(calls, 3)
            self.assertEqual(sleeps, [2.0, 4.0])
            self.assertEqual(list(root.iterdir()), [])

    def test_unit_is_transient_download_error_table(self) -> None:
        """Classify each error class and HTTP status against the retry allow-list."""
        def make_http_error(code: int) -> urllib.error.HTTPError:
            err = urllib.error.HTTPError(
                url="https://example.com/asset",
                code=code,
                msg="error",
                hdrs={},
                fp=None,
            )
            self.addCleanup(err.close)
            return err

        cases: list[tuple[BaseException, bool]] = [
            (make_http_error(408), True),
            (make_http_error(429), True),
            (make_http_error(500), True),
            (make_http_error(503), True),
            (make_http_error(400), False),
            (make_http_error(403), False),
            (make_http_error(404), False),
            (urllib.error.URLError("connection refused"), True),
            (TimeoutError("timed out"), True),
            (ConnectionResetError("connection reset"), True),
            (http.client.IncompleteRead(b""), True),
            (OSError(errno.ENOSPC, "No space left on device"), False),
            (PermissionError("permission denied"), False),
            (FileNotFoundError("file not found"), False),
        ]
        for error, expected in cases:
            with self.subTest(error=error, expected=expected):
                self.assertIs(pact_ffi.is_transient_download_error(error), expected)

    def test_workflow_pact_ffi_install_steps_preceded_by_cache(self) -> None:
        """Require a matching pinned cache step before every Pact FFI install step."""
        workflows = (
            ROOT / ".github" / "workflows" / "ci.yml",
            ROOT / ".github" / "workflows" / "portability.yml",
        )
        total_pact_steps = 0
        cache_action = "actions/cache@55cc8345863c7cc4c66a329aec7e433d2d1c52a9"
        cache_path = ".workingdir/cache/pact-ffi"
        cache_key = "pact-ffi-${{ runner.os }}-${{ runner.arch }}-${{ steps.pact-ffi-asset.outputs.sha256 }}"
        for path in workflows:
            text = path.read_text(encoding="utf-8")
            self.assertNotIn("hashFiles('scripts/ci/pact_ffi.py')", text)
            steps = re.split(r"(?m)^ {6}- ", text)
            for index, step in enumerate(steps[1:], 1):
                if not ("scripts/ci/pact_ffi.py" in step and "--lib-dir" in step):
                    continue
                total_pact_steps += 1
                prev_step = steps[index - 1]
                with self.subTest(workflow=path.name, step_index=index):
                    self.assertIn(cache_action, prev_step)
                    self.assertIn(f"path: {cache_path}", prev_step)
                    self.assertIn(f"key: {cache_key}", prev_step)
                    self.assertNotIn("restore-keys", prev_step)
                    self.assertTrue(
                        any(
                            "id: pact-ffi-asset" in s and "--asset-digest" in s
                            for s in steps[:index]
                        )
                    )
                    install_if = re.search(r"(?m)^ +if: +(.*)$", step)
                    cache_if = re.search(r"(?m)^ +if: +(.*)$", prev_step)
                    if install_if is None or cache_if is None:
                        self.fail("expected if condition on install and cache steps")
                    self.assertEqual(cache_if.group(1).strip(), install_if.group(1).strip())
        self.assertEqual(total_pact_steps, 4)

    def _assert_pact_step_exports_tmpdir(self, step: str) -> None:
        """Require Unix Pact installs to create and export temp storage under RUNNER_TEMP."""
        self.assertIn('install -d -m 0700 "$RUNNER_TEMP/tmp"', step)
        self.assertRegex(
            step,
            r'(?m)^\s*echo "TMPDIR=\$RUNNER_TEMP/tmp" >> "\$GITHUB_ENV"$',
        )

    def test_workflow_pact_ffi_install_steps_export_safe_tmpdir(self) -> None:
        """Keep every Unix Pact install's Go temp storage outside the scanned /tmp tree."""
        install_steps = 0
        for name in ("ci.yml", "portability.yml"):
            text = (ROOT / ".github" / "workflows" / name).read_text(encoding="utf-8")
            for job in re.split(r"(?m)^  (?=[\w-]+:\s*$)", text):
                for step in re.split(r"(?m)^ {6}- ", job)[1:]:
                    if "scripts/ci/pact_ffi.py" not in step or "--lib-dir" not in step:
                        continue
                    if "shell: pwsh" in step:
                        continue
                    install_steps += 1
                    with self.subTest(workflow=name, job=job.splitlines()[0], step=step.splitlines()[0]):
                        self._assert_pact_step_exports_tmpdir(step)
        self.assertEqual(install_steps, 3)

    def test_positive_workflow_pact_tmpdir_export(self) -> None:
        """Accept a synthetic Unix step creating and exporting RUNNER_TEMP/tmp."""
        step = (
            'name: Install synthetic Pact FFI\nrun: |\n'
            '  install -d -m 0700 "$RUNNER_TEMP/tmp"\n'
            '  echo "TMPDIR=$RUNNER_TEMP/tmp" >> "$GITHUB_ENV"\n'
        )
        self._assert_pact_step_exports_tmpdir(step)

    def test_negative_workflow_pact_tmpdir_missing_or_under_tmp(self) -> None:
        """Reject synthetic steps missing TMPDIR or placing it in the scanned /tmp tree."""
        step = 'name: Install synthetic Pact FFI\nrun: |\n  install -d -m 0700 "$RUNNER_TEMP/tmp"\n'
        for value in (None, "/tmp", "/tmp/", "/tmp/go-build"):  # noqa: S108 - refused TMPDIR values, never opened. # nosec B108
            with self.subTest(value=value), self.assertRaisesRegex(AssertionError, "TMPDIR"):
                export = "" if value is None else f'  echo "TMPDIR={value}" >> "$GITHUB_ENV"\n'
                self._assert_pact_step_exports_tmpdir(step + export)

    def test_boundary_pin_change_downloads_new_archive_beside_stale_one(self) -> None:
        """Download new asset when pin changes while keeping stale archive alongside."""
        old = gzip.compress(b"old pinned ffi")
        new = gzip.compress(b"new pinned ffi")
        old_asset = pact_ffi.Asset("fixture.so.gz", hashlib.sha256(old).hexdigest(), "libpact_ffi.so")
        new_asset = pact_ffi.Asset("fixture.so.gz", hashlib.sha256(new).hexdigest(), "libpact_ffi.so")
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            self._cache_install(root, old_asset, old)
            self.assertEqual(self._cache_install(root, new_asset, new).read_bytes(), b"new pinned ffi")
            self.assertEqual(self.download_calls, 1)

    def test_boundary_cache_dir_equals_library_dir_works(self) -> None:
        """Allow cache_dir and library_dir to match, as used by the Makefile target."""
        compressed = gzip.compress(b"verified pact ffi")
        asset = pact_ffi.Asset(
            filename="fixture.so.gz",
            sha256=hashlib.sha256(compressed).hexdigest(),
            library="libpact_ffi.so",
        )
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            combined = root / "pact-ffi"
            installed = self._cache_install(
                root, asset, compressed, cache_dir=combined, library_dir=combined
            )
            self.assertEqual(installed.read_bytes(), b"verified pact ffi")
            self.assertTrue(pact_ffi.cached_archive_path(asset, combined).is_file())
            self.assertTrue((combined / asset.library).is_file())

    def test_negative_symlinked_cache_directory_outside_root_is_refused(self) -> None:
        """Refuse a cache directory whose symlink resolves outside cache_root."""
        with tempfile.TemporaryDirectory() as directory, tempfile.TemporaryDirectory() as outside:
            root = Path(directory)
            try:
                (root / "cache").symlink_to(outside, target_is_directory=True)
            except OSError as error:
                # Windows without Developer Mode or SeCreateSymbolicLinkPrivilege refuses symlinks.
                self.skipTest(f"symlink creation unavailable on this host: {error}")
            with self.assertRaisesRegex(pact_ffi.InstallError, "resolves outside"):
                pact_ffi.install(
                    ROOT / "testutil/pact/go.mod", root / "lib", cache_dir=root / "cache", cache_root=root
                )
            self.assertEqual(list(Path(outside).iterdir()), [])

    def test_negative_cache_dir_without_cache_root_raises_install_error(self) -> None:
        """Refuse installation when cache_dir is specified without cache_root."""
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            with self.assertRaisesRegex(pact_ffi.InstallError, "cache_root"):
                pact_ffi.install(
                    ROOT / "testutil/pact/go.mod",
                    root / "lib",
                    cache_dir=root / "cache",
                    cache_root=None,
                )

    def test_negative_interrupted_download_removes_temporary(self) -> None:
        asset = pact_ffi.Asset("fixture.so.gz", "0" * 64, "libpact_ffi.so")
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            with (
                mock.patch.object(
                    pact_ffi.urllib.request, "urlopen", side_effect=KeyboardInterrupt
                ),
                self.assertRaises(KeyboardInterrupt),
            ):
                pact_ffi.download_archive(asset, root)
            self.assertEqual(list(root.iterdir()), [])

    def test_negative_keep_and_cleanup_failures_raise_one_install_error(self) -> None:
        compressed = gzip.compress(b"verified pact ffi")
        asset = pact_ffi.Asset("fixture.so.gz", hashlib.sha256(compressed).hexdigest(), "libpact_ffi.so")
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            with (
                mock.patch.object(
                    pact_ffi.urllib.request, "urlopen", return_value=io.BytesIO(compressed)
                ),
                mock.patch.object(pact_ffi.os, "replace", side_effect=OSError("replace denied")),
                mock.patch.object(Path, "unlink", side_effect=OSError("unlink denied")),
                self.assertRaisesRegex(pact_ffi.InstallError, "replace denied; unlink .*unlink denied"),
            ):
                pact_ffi.cached_archive(asset, root)

    def test_local_gates_install_pact_ffi_into_ignored_repository_directory(self) -> None:
        """Verify the Makefile targets install Pact FFI into .workingdir/cache/pact-ffi."""
        makefile = (ROOT / "Makefile").read_text(encoding="utf-8")
        targets = next(
            line.split(":=", 1)[1].split()
            for line in makefile.splitlines()
            if line.startswith("PACT_FFI_TARGETS :=")
        )
        for target in (
            "verify-all",
            "test-all",
            "ci-all",
            "portability-test",
            "build-all",
            "fault-recovery",
        ):
            self.assertIn(target, targets)
        self.assertIn("PACT_FFI_DIR := $(CURDIR)/.workingdir/cache/pact-ffi", makefile)
        self.assertIn(
            'scripts/ci/pact_ffi.py --cache-dir "$(PACT_FFI_DIR)" --lib-dir "$(PACT_FFI_DIR)"',
            makefile,
        )
        self.assertIn("$(PACT_FFI_TARGETS): pact-ffi", makefile)
        self.assertIn(
            "$(PACT_FFI_TARGETS): override PACT_GO_LIB_DOWNLOAD_PATH := $(PACT_FFI_DIR)\n"
            "ifeq ($(origin PACT_GO_LIB_DOWNLOAD_PATH),command line)\n"
            "export PACT_GO_LIB_DOWNLOAD_PATH\n"
            "else\n"
            "$(PACT_FFI_TARGETS): export PACT_GO_LIB_DOWNLOAD_PATH := $(PACT_FFI_DIR)\n"
            "endif",
            makefile,
        )
        self.assertIn("'-L$(PACT_FFI_DIR)' '-Wl,-rpath,$(PACT_FFI_DIR)'", makefile)
        self.assertIn("override export PATH := $(PACT_FFI_DIR)$(PACT_PATH_SEP)$(PATH)", makefile)
        ignored = (ROOT / ".gitignore").read_text(encoding="utf-8").splitlines()
        self.assertIn(".workingdir/", ignored)

    def test_positive_local_pact_modifier_pairs(self) -> None:
        """Require origin-guarded export blocks outside the Windows branch."""
        makefile = (ROOT / "Makefile").read_text(encoding="utf-8")
        self.assertEqual(pact_modifier_violations(makefile), [])

    def test_negative_local_pact_combined_modifiers(self) -> None:
        """Reject a TMPDIR assignment folded into one override export line."""
        makefile = (ROOT / "Makefile").read_text(encoding="utf-8")
        override = "$(PACT_FFI_TARGETS): override TMPDIR := $(PACT_FFI_TMPDIR)\n"
        export = "$(PACT_FFI_TARGETS): export TMPDIR := $(PACT_FFI_TMPDIR)\n"
        guard = "ifeq ($(origin TMPDIR),command line)\nexport TMPDIR\nelse\n"
        combined = override.replace(": override ", ": override export ")
        mutant = makefile.replace(override + guard + export + "endif\n", combined)
        self.assertIn(combined.rstrip(), pact_modifier_violations(mutant))

    def test_negative_local_pact_reversed_modifier_pair(self) -> None:
        """Reject a Pact download path export placed before its override line."""
        makefile = (ROOT / "Makefile").read_text(encoding="utf-8")
        override = "$(PACT_FFI_TARGETS): override PACT_GO_LIB_DOWNLOAD_PATH := $(PACT_FFI_DIR)\n"
        export = "$(PACT_FFI_TARGETS): export PACT_GO_LIB_DOWNLOAD_PATH := $(PACT_FFI_DIR)\n"
        guard = "ifeq ($(origin PACT_GO_LIB_DOWNLOAD_PATH),command line)\nexport PACT_GO_LIB_DOWNLOAD_PATH\nelse\n"
        mutant = makefile.replace(override + guard + export + "endif\n", export + override)
        self.assertIn(override.rstrip(), pact_modifier_violations(mutant))

    def test_negative_local_pact_adjacent_modifier_pair(self) -> None:
        """Reject adjacent CGO_LDFLAGS modifiers that lose caller overrides on make 3.81."""
        makefile = (ROOT / "Makefile").read_text(encoding="utf-8")
        value = "$(strip $(CGO_LDFLAGS) '-L$(PACT_FFI_DIR)' '-Wl,-rpath,$(PACT_FFI_DIR)')\n"
        override = "$(PACT_FFI_TARGETS): override CGO_LDFLAGS := " + value
        export = "$(PACT_FFI_TARGETS): export CGO_LDFLAGS := " + value
        guard = "ifeq ($(origin CGO_LDFLAGS),command line)\nexport CGO_LDFLAGS\nelse\n"
        mutant = makefile.replace(override + guard + export + "endif\n", override + export)
        self.assertIn(override.rstrip(), pact_modifier_violations(mutant))

    def _assert_makefile_pact_tmpdir(self, makefile: str) -> None:
        """Require Unix Pact targets to export safe temp storage created before installation."""
        self.assertIn(
            "PACT_FFI_TMPDIR := $(or $(XDG_CACHE_HOME),$(HOME)/.cache)/golusoris/tmp",
            makefile,
        )
        unix = makefile.split("ifeq ($(OS),Windows_NT)\n", 1)[1].split("\nelse\n", 1)[1]
        self.assertIn("ifeq ($(filter-out /tmp /tmp/,$(strip $(TMPDIR))),)", unix)
        self.assertIn(
            "$(PACT_FFI_TARGETS): override TMPDIR := $(PACT_FFI_TMPDIR)\n"
            "ifeq ($(origin TMPDIR),command line)\n"
            "export TMPDIR\n"
            "else\n"
            "$(PACT_FFI_TARGETS): export TMPDIR := $(PACT_FFI_TMPDIR)\n"
            "endif",
            unix,
        )
        goal_guard = "ifneq ($(filter $(PACT_FFI_TARGETS),$(MAKECMDGOALS)),)"
        self.assertIn(goal_guard, unix)
        creation = unix.split(goal_guard + "\n", 1)[1]
        self.assertIn('$(shell mkdir -p -m 0700 "$(PACT_FFI_TMPDIR)")', creation)
        self.assertRegex(
            creation,
            r'(?m)^ifneq \(\$\(shell test -d "\$\(PACT_FFI_TMPDIR\)" && echo ok\),ok\)\n'
            r"\$\(error [^\n]*\$\(PACT_FFI_TMPDIR\)[^\n]*\)$",
        )
        recipe = makefile.split("\npact-ffi:", 1)[1].split("\n\n", 1)[0]
        self.assertNotIn("mkdir", recipe)

    def test_local_pact_targets_export_safe_tmpdir(self) -> None:
        """Require local Unix Pact targets to preserve custom TMPDIR and create the fallback."""
        self._assert_makefile_pact_tmpdir((ROOT / "Makefile").read_text(encoding="utf-8"))

    def test_negative_local_pact_tmpdir_export_missing(self) -> None:
        """Reject Makefile text with the target-specific TMPDIR export removed."""
        makefile = (ROOT / "Makefile").read_text(encoding="utf-8")
        mutant = makefile.replace("$(PACT_FFI_TARGETS): export TMPDIR := $(PACT_FFI_TMPDIR)\n", "")
        with self.assertRaisesRegex(AssertionError, "export TMPDIR"):
            self._assert_makefile_pact_tmpdir(mutant)

    def _make_tmpdir_dry_run(self, goal: str, directory: str, *, tmpdir: str | None = None) -> Path:
        """Dry-run a named goal with isolated home and cache directories."""
        self._make_dry_run(goal, directory, tmpdir=tmpdir)
        return Path(directory) / "golusoris" / "tmp"

    def _make_dry_run(
        self, goal: str, directory: str, *, tmpdir: str | None = None, assignments: tuple[str, ...] = ()
    ) -> subprocess.CompletedProcess[str]:
        """Dry-run a fixed goal and print inherited Pact variables from an isolated prerequisite."""
        environment = os.environ.copy()
        environment.pop("TMPDIR", None)
        environment.pop("CGO_LDFLAGS", None)
        environment.pop("MAKEFLAGS", None)
        environment.pop("MAKEOVERRIDES", None)
        environment.update(HOME=directory, XDG_CACHE_HOME=directory)
        if tmpdir is not None:
            environment["TMPDIR"] = tmpdir
        make = shutil.which("make")
        self.assertIsNotNone(make)
        probe = Path(directory) / "pact-environment.mk"
        probe.write_text(
            ".PHONY: pact-environment\n"
            "portability-test: pact-environment\n"
            "pact-environment:\n"
            "\t+@printf '%s\\n' \"PACT_ENV_CGO_LDFLAGS=$$CGO_LDFLAGS\" "
            '"PACT_ENV_PACT_GO_LIB_DOWNLOAD_PATH=$$PACT_GO_LIB_DOWNLOAD_PATH" '
            '"PACT_ENV_TMPDIR=$$TMPDIR"\n',
            encoding="utf-8",
        )
        result = subprocess.run(  # noqa: S603 - resolved make, dry run of a fixed repository goal. # nosec B603
            [str(make), "-n", "-j2", "-f", "Makefile", "-f", str(probe), goal, *assignments],
            cwd=ROOT,
            env=environment,
            capture_output=True,
            text=True,
            timeout=30,
            check=False,
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        return result

    def _pact_dry_run_environment(self, directory: str, assignments: tuple[str, ...]) -> dict[str, str]:
        """Read marked prerequisite environment lines from the shared make dry run."""
        result = self._make_dry_run("portability-test", directory, assignments=assignments)
        return dict(
            line.removeprefix("PACT_ENV_").split("=", 1)
            for line in result.stdout.splitlines()
            if line.startswith("PACT_ENV_")
        )

    @skipUnless(os.name == "posix" and shutil.which("make"), "POSIX make is required")
    def test_positive_local_pact_command_line_exports(self) -> None:
        """Export Pact flags and safe paths to prerequisites despite command-line assignments."""
        with tempfile.TemporaryDirectory() as directory:
            environment = self._pact_dry_run_environment(
                directory,
                ("CGO_LDFLAGS=-Lcaller", f"PACT_GO_LIB_DOWNLOAD_PATH={directory}/caller", f"TMPDIR={UNSAFE_TMPDIR}"),
            )
            self.assertTrue(environment["CGO_LDFLAGS"].startswith("-Lcaller "), environment)
            self.assertIn(f"-L{ROOT / '.workingdir/cache/pact-ffi'}", environment["CGO_LDFLAGS"])
            self.assertEqual(environment["PACT_GO_LIB_DOWNLOAD_PATH"], str(ROOT / ".workingdir/cache/pact-ffi"))
            self.assertEqual(environment["TMPDIR"], str(Path(directory) / "golusoris/tmp"))

    @skipUnless(os.name == "posix" and shutil.which("make"), "POSIX make is required")
    def test_boundary_local_pact_empty_command_line_flags(self) -> None:
        """Export quoted Pact linker flags without leading whitespace for an empty caller value."""
        with tempfile.TemporaryDirectory() as directory:
            environment = self._pact_dry_run_environment(directory, ("CGO_LDFLAGS=",))
            self.assertTrue(environment["CGO_LDFLAGS"].startswith("'-L"), environment)

    @skipUnless(os.name == "posix" and shutil.which("make"), "POSIX make is required")
    def test_negative_local_pact_unset_caller_flags(self) -> None:
        """Print inherited Pact flags without caller flags when the environment value is unset."""
        with tempfile.TemporaryDirectory() as directory:
            environment = self._pact_dry_run_environment(directory, ())
            self.assertIn("CGO_LDFLAGS", environment)
            self.assertNotIn("-Lcaller", environment["CGO_LDFLAGS"])

    @skipUnless(os.name == "posix" and shutil.which("make"), "POSIX make is required")
    def test_positive_local_pact_tmpdir_created_before_recipes(self) -> None:
        """Create private fallback storage while parsing a Pact target dry run."""
        with tempfile.TemporaryDirectory() as directory:
            path = self._make_tmpdir_dry_run("portability-test", directory)
            self.assertTrue(path.is_dir(), str(path))
            self.assertEqual(path.stat().st_mode & 0o777, 0o700)

    @skipUnless(os.name == "posix" and shutil.which("make"), "POSIX make is required")
    def test_positive_local_pact_tmpdir_cache_path_with_space(self) -> None:
        """Create private fallback storage when home and cache paths contain a space."""
        with tempfile.TemporaryDirectory(prefix="golusoris-home with-space-") as directory:
            path = self._make_tmpdir_dry_run("portability-test", directory)
            self.assertTrue(path.is_dir(), str(path))
            self.assertEqual(path.stat().st_mode & 0o777, 0o700)

    @skipUnless(os.name == "posix" and shutil.which("make"), "POSIX make is required")
    def test_negative_local_pact_tmpdir_unrelated_goal(self) -> None:
        """Leave fallback storage absent when parsing an unrelated goal."""
        with tempfile.TemporaryDirectory() as directory:
            path = self._make_tmpdir_dry_run("modules-list", directory)
            self.assertFalse(path.exists(), str(path))

    @skipUnless(os.name == "posix" and shutil.which("make"), "POSIX make is required")
    def test_negative_local_pact_tmpdir_custom_directory(self) -> None:
        """Preserve an existing caller TMPDIR without creating fallback storage."""
        with tempfile.TemporaryDirectory() as directory, tempfile.TemporaryDirectory() as custom:
            path = self._make_tmpdir_dry_run("portability-test", directory, tmpdir=custom)
            self.assertFalse(path.exists(), str(path))

    def test_truncated_download_is_wrapped_and_removed(self) -> None:
        """Capture sleep backoff and stderr during transient truncated download failures."""
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
            sleeps: list[float] = []
            with (
                mock.patch.object(
                    pact_ffi.urllib.request,
                    "urlopen",
                    return_value=TruncatedResponse(),
                ),
                mock.patch.object(pact_ffi, "SLEEP", side_effect=sleeps.append),
                contextlib.redirect_stderr(io.StringIO()),
                self.assertRaisesRegex(pact_ffi.InstallError, "download failed"),
            ):
                pact_ffi.download_archive(asset, root)
            self.assertEqual(sleeps, [2.0, 4.0])
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

    def test_asset_digest_prints_pinned_sha256_for_supported_platforms(self) -> None:
        """Print the 64-hex SHA-256 lock digest for supported platforms and exit 0."""
        cases = (
            ("Darwin", "arm64", pact_ffi.ASSETS[("Darwin", "aarch64")].sha256),
            ("Linux", "x86_64", pact_ffi.ASSETS[("Linux", "x86_64")].sha256),
        )
        for system_name, machine, expected_digest in cases:
            with self.subTest(system=system_name, machine=machine):
                stdout = io.StringIO()
                with (
                    mock.patch.object(pact_ffi.sys, "argv", ["pact_ffi.py", "--asset-digest"]),
                    mock.patch.object(pact_ffi.platform, "system", return_value=system_name),
                    mock.patch.object(pact_ffi.platform, "machine", return_value=machine),
                    contextlib.redirect_stdout(stdout),
                ):
                    exit_code = pact_ffi.main()
                self.assertEqual(exit_code, 0)
                self.assertEqual(stdout.getvalue().strip(), expected_digest)
                self.assertRegex(stdout.getvalue().strip(), r"^[0-9a-f]{64}$")

    def test_asset_digest_unsupported_platform_reports_error_and_exits_code_three(self) -> None:
        """Report failure on stderr and return EXIT_ERROR when the platform is unsupported."""
        stderr = io.StringIO()
        with (
            mock.patch.object(pact_ffi.sys, "argv", ["pact_ffi.py", "--asset-digest"]),
            mock.patch.object(pact_ffi.platform, "system", return_value="FreeBSD"),
            mock.patch.object(pact_ffi.platform, "machine", return_value="x86_64"),
            contextlib.redirect_stderr(stderr),
        ):
            exit_code = pact_ffi.main()
        self.assertEqual(exit_code, pact_ffi.EXIT_ERROR)
        self.assertIn("Pact FFI install failed: unsupported Pact FFI host", stderr.getvalue())

    def test_main_without_lib_dir_and_without_asset_digest_exits_code_two(self) -> None:
        """Exit with code 2 via argparse error when neither --lib-dir nor --asset-digest is given."""
        with (
            mock.patch.object(pact_ffi.sys, "argv", ["pact_ffi.py"]),
            contextlib.redirect_stderr(io.StringIO()),
        ):
            with self.assertRaises(SystemExit) as error:
                pact_ffi.main()
            self.assertEqual(error.exception.code, 2)


if __name__ == "__main__":
    main(verbosity=2)
