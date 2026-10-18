#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Positive, negative, and boundary tests for the release binary smoke driver."""

from __future__ import annotations

import contextlib
import io
import json
import subprocess
import sys
import tempfile
from collections.abc import Callable
from pathlib import Path
from unittest import TestCase, main

import release_smoke


class ReleaseSmokeTest(TestCase):
    """Test suite for release_smoke.py."""

    def setUp(self) -> None:
        self.temp_dir = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp_dir.cleanup)
        self.suite_root = Path(self.temp_dir.name)
        self.dist_dir = self.suite_root / "dist"
        self.dist_dir.mkdir(parents=True, exist_ok=True)

    def write_artifacts(self, entries: list[dict[str, object]]) -> Path:
        artifacts_path = self.dist_dir / "artifacts.json"
        artifacts_path.write_text(json.dumps(entries), encoding="utf-8")
        return artifacts_path

    def run_smoke(
        self,
        argv: list[str],
        runner: Callable[[str], tuple[int, str]] | None = None,
    ) -> tuple[int, str, str]:
        stdout = io.StringIO()
        stderr = io.StringIO()
        with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
            code = release_smoke.main(argv, runner=runner or release_smoke.run_binary)
        return code, stdout.getvalue(), stderr.getvalue()

    def test_positive_linux_amd64(self) -> None:
        self.write_artifacts([
            {
                "name": "golusoris",
                "path": "dist/golusoris_linux_amd64_v1/golusoris",
                "goos": "linux",
                "goarch": "amd64",
                "type": "Binary",
                "extra": {"ID": "golusoris"},
            },
            {
                "name": "golusoris-mcp",
                "path": "dist/golusoris-mcp_linux_amd64_v1/golusoris-mcp",
                "goos": "linux",
                "goarch": "amd64",
                "type": "Binary",
                "extra": {"ID": "golusoris-mcp"},
            },
        ])
        core_out = "golusoris version 1.2.3 (commit abc1234, built 2026-10-13T00:00:00Z)"
        mcp_out = "golusoris-mcp version 1.2.3"

        def fake_runner(path: str) -> tuple[int, str]:
            if "golusoris-mcp" in path:
                return 0, mcp_out
            return 0, core_out

        code, out, err = self.run_smoke(
            ["--version", "v1.2.3", "--target", "linux/amd64", "--dist", str(self.dist_dir)],
            runner=fake_runner,
        )
        self.assertEqual(code, 0)
        self.assertIn(f"release-smoke: {core_out}\n", out)
        self.assertIn(f"release-smoke: {mcp_out}\n", out)
        self.assertEqual(err, "")

    def test_positive_windows_arm64(self) -> None:
        self.write_artifacts([
            {
                "name": "golusoris",
                "path": "dist/golusoris_windows_arm64_v1/golusoris.exe",
                "goos": "windows",
                "goarch": "arm64",
                "type": "Binary",
                "extra": {"ID": "golusoris"},
            },
            {
                "name": "golusoris-mcp",
                "path": "dist/golusoris-mcp_windows_arm64_v1/golusoris-mcp.exe",
                "goos": "windows",
                "goarch": "arm64",
                "type": "Binary",
                "extra": {"ID": "golusoris-mcp"},
            },
        ])

        def fake_runner(path: str) -> tuple[int, str]:
            name = "golusoris-mcp" if "golusoris-mcp" in path else "golusoris"
            return 0, f"{name} version 1.2.3"

        code, out, err = self.run_smoke(
            ["--version", "1.2.3", "--target", "windows/arm64", "--dist", str(self.dist_dir)],
            runner=fake_runner,
        )
        self.assertEqual(code, 0)
        self.assertIn("release-smoke: golusoris version 1.2.3\n", out)
        self.assertIn("release-smoke: golusoris-mcp version 1.2.3\n", out)
        self.assertEqual(err, "")

    def test_positive_darwin_amd64(self) -> None:
        self.write_artifacts([
            {
                "name": "golusoris",
                "path": "dist/golusoris_darwin_amd64_v1/golusoris",
                "goos": "darwin",
                "goarch": "amd64",
                "type": "Binary",
                "extra": {"ID": "golusoris"},
            },
            {
                "name": "golusoris-mcp",
                "path": "dist/golusoris-mcp_darwin_amd64_v1/golusoris-mcp",
                "goos": "darwin",
                "goarch": "amd64",
                "type": "Binary",
                "extra": {"ID": "golusoris-mcp"},
            },
        ])

        def fake_runner(path: str) -> tuple[int, str]:
            name = "golusoris-mcp" if "golusoris-mcp" in path else "golusoris"
            return 0, f"{name} version 1.2.3"

        code, out, err = self.run_smoke(
            ["--version", "v1.2.3", "--target", "darwin/amd64", "--dist", str(self.dist_dir)],
            runner=fake_runner,
        )
        self.assertEqual(code, 0)
        self.assertIn("release-smoke: golusoris version 1.2.3\n", out)
        self.assertIn("release-smoke: golusoris-mcp version 1.2.3\n", out)
        self.assertEqual(err, "")

    def test_dev_version(self) -> None:
        self.write_artifacts([
            {
                "name": "golusoris",
                "path": "dist/golusoris_linux_amd64_v1/golusoris",
                "goos": "linux",
                "goarch": "amd64",
                "type": "Binary",
                "extra": {"ID": "golusoris"},
            },
            {
                "name": "golusoris-mcp",
                "path": "dist/golusoris-mcp_linux_amd64_v1/golusoris-mcp",
                "goos": "linux",
                "goarch": "amd64",
                "type": "Binary",
                "extra": {"ID": "golusoris-mcp"},
            },
        ])

        def fake_runner(path: str) -> tuple[int, str]:
            if "golusoris-mcp" in path:
                return 0, "golusoris-mcp version 1.2.3"
            return 0, "golusoris version dev (commit none, built unknown)"

        code, _, err = self.run_smoke(
            ["--version", "v1.2.3", "--target", "linux/amd64", "--dist", str(self.dist_dir)],
            runner=fake_runner,
        )
        self.assertEqual(code, 1)
        self.assertIn('want "golusoris version 1.2.3"', err)
        self.assertIn("1 of 2 binaries failed", err)

    def test_prefix_collision(self) -> None:
        self.write_artifacts([
            {
                "name": "golusoris",
                "path": "dist/golusoris_linux_amd64_v1/golusoris",
                "goos": "linux",
                "goarch": "amd64",
                "type": "Binary",
                "extra": {"ID": "golusoris"},
            },
            {
                "name": "golusoris-mcp",
                "path": "dist/golusoris-mcp_linux_amd64_v1/golusoris-mcp",
                "goos": "linux",
                "goarch": "amd64",
                "type": "Binary",
                "extra": {"ID": "golusoris-mcp"},
            },
        ])

        def fake_runner(path: str) -> tuple[int, str]:
            if "golusoris-mcp" in path:
                return 0, "golusoris-mcp version 1.2.30"
            return 0, "golusoris version 1.2.3"

        code, _, err = self.run_smoke(
            ["--version", "v1.2.3", "--target", "linux/amd64", "--dist", str(self.dist_dir)],
            runner=fake_runner,
        )
        self.assertEqual(code, 1)
        self.assertIn('want "golusoris-mcp version 1.2.3"', err)
        self.assertIn("1 of 2 binaries failed", err)

    def test_runner_exit_1(self) -> None:
        self.write_artifacts([
            {
                "name": "golusoris",
                "path": "dist/golusoris_linux_amd64_v1/golusoris",
                "goos": "linux",
                "goarch": "amd64",
                "type": "Binary",
                "extra": {"ID": "golusoris"},
            },
            {
                "name": "golusoris-mcp",
                "path": "dist/golusoris-mcp_linux_amd64_v1/golusoris-mcp",
                "goos": "linux",
                "goarch": "amd64",
                "type": "Binary",
                "extra": {"ID": "golusoris-mcp"},
            },
        ])

        def fake_runner(path: str) -> tuple[int, str]:
            if "golusoris-mcp" in path:
                return 1, "Error: unknown flag: --version"
            return 0, "golusoris version 1.2.3"

        code, _, err = self.run_smoke(
            ["--version", "v1.2.3", "--target", "linux/amd64", "--dist", str(self.dist_dir)],
            runner=fake_runner,
        )
        self.assertEqual(code, 1)
        self.assertIn("golusoris-mcp --version failed: Error: unknown flag: --version", err)
        self.assertIn("1 of 2 binaries failed", err)

    def test_binary_missing_for_target(self) -> None:
        self.write_artifacts([
            {
                "name": "golusoris",
                "path": "dist/golusoris_linux_amd64_v1/golusoris",
                "goos": "linux",
                "goarch": "amd64",
                "type": "Binary",
                "extra": {"ID": "golusoris"},
            },
        ])

        def fake_runner(path: str) -> tuple[int, str]:
            del path
            return 0, "golusoris version 1.2.3"

        code, _, err = self.run_smoke(
            ["--version", "v1.2.3", "--target", "linux/amd64", "--dist", str(self.dist_dir)],
            runner=fake_runner,
        )
        self.assertEqual(code, 1)
        self.assertIn("golusoris-mcp: expected one linux/amd64 binary, found 0", err)
        self.assertIn("1 of 2 binaries failed", err)

    def test_two_binaries_for_one_target(self) -> None:
        self.write_artifacts([
            {
                "name": "golusoris",
                "path": "dist/golusoris_linux_amd64_v1/golusoris",
                "goos": "linux",
                "goarch": "amd64",
                "type": "Binary",
                "extra": {"ID": "golusoris"},
            },
            {
                "name": "golusoris",
                "path": "dist/golusoris_linux_amd64_v2/golusoris",
                "goos": "linux",
                "goarch": "amd64",
                "type": "Binary",
                "extra": {"ID": "golusoris"},
            },
            {
                "name": "golusoris-mcp",
                "path": "dist/golusoris-mcp_linux_amd64_v1/golusoris-mcp",
                "goos": "linux",
                "goarch": "amd64",
                "type": "Binary",
                "extra": {"ID": "golusoris-mcp"},
            },
        ])

        def fake_runner(path: str) -> tuple[int, str]:
            del path
            return 0, "golusoris-mcp version 1.2.3"

        code, _, err = self.run_smoke(
            ["--version", "v1.2.3", "--target", "linux/amd64", "--dist", str(self.dist_dir)],
            runner=fake_runner,
        )
        self.assertEqual(code, 1)
        self.assertIn("golusoris: expected one linux/amd64 binary, found 2", err)
        self.assertIn("1 of 2 binaries failed", err)

    def test_empty_artifacts_json(self) -> None:
        artifacts = self.dist_dir / "artifacts.json"
        artifacts.write_text("", encoding="utf-8")
        code, _, err = self.run_smoke(
            ["--version", "v1.2.3", "--target", "linux/amd64", "--dist", str(self.dist_dir)],
        )
        self.assertEqual(code, 1)
        self.assertIn("missing or empty", err)

    def test_missing_artifacts_file(self) -> None:
        missing_dir = self.suite_root / "nonexistent"
        code, _, err = self.run_smoke(
            ["--version", "v1.2.3", "--target", "linux/amd64", "--dist", str(missing_dir)],
        )
        self.assertEqual(code, 1)
        self.assertIn("missing or empty", err)

    def test_path_with_parent_reference(self) -> None:
        self.write_artifacts([
            {
                "name": "golusoris",
                "path": "dist/../golusoris",
                "goos": "linux",
                "goarch": "amd64",
                "type": "Binary",
                "extra": {"ID": "golusoris"},
            },
            {
                "name": "golusoris-mcp",
                "path": "dist/golusoris-mcp_linux_amd64_v1/golusoris-mcp",
                "goos": "linux",
                "goarch": "amd64",
                "type": "Binary",
                "extra": {"ID": "golusoris-mcp"},
            },
        ])
        code, _, err = self.run_smoke(
            ["--version", "v1.2.3", "--target", "linux/amd64", "--dist", str(self.dist_dir)],
        )
        self.assertEqual(code, 1)
        self.assertIn("must be relative and contain no '..'", err)

    def test_absolute_path(self) -> None:
        self.write_artifacts([
            {
                "name": "golusoris",
                "path": "/usr/local/bin/golusoris",
                "goos": "linux",
                "goarch": "amd64",
                "type": "Binary",
                "extra": {"ID": "golusoris"},
            },
            {
                "name": "golusoris-mcp",
                "path": "dist/golusoris-mcp_linux_amd64_v1/golusoris-mcp",
                "goos": "linux",
                "goarch": "amd64",
                "type": "Binary",
                "extra": {"ID": "golusoris-mcp"},
            },
        ])
        code, _, err = self.run_smoke(
            ["--version", "v1.2.3", "--target", "linux/amd64", "--dist", str(self.dist_dir)],
        )
        self.assertEqual(code, 1)
        self.assertIn("must be relative and contain no '..'", err)

    def test_bad_target(self) -> None:
        code, _, err = self.run_smoke(
            ["--version", "v1.2.3", "--target", "solaris/sparc", "--dist", str(self.dist_dir)],
        )
        self.assertEqual(code, 2)
        self.assertIn("invalid target 'solaris/sparc'", err)

    def test_empty_version_tag(self) -> None:
        code, _, err = self.run_smoke(
            ["--version", "v", "--target", "linux/amd64", "--dist", str(self.dist_dir)],
        )
        self.assertEqual(code, 2)
        self.assertIn("empty version in tag 'v'", err)

    def test_timeout(self) -> None:
        self.write_artifacts([
            {
                "name": "golusoris",
                "path": "dist/golusoris_linux_amd64_v1/golusoris",
                "goos": "linux",
                "goarch": "amd64",
                "type": "Binary",
                "extra": {"ID": "golusoris"},
            },
            {
                "name": "golusoris-mcp",
                "path": "dist/golusoris-mcp_linux_amd64_v1/golusoris-mcp",
                "goos": "linux",
                "goarch": "amd64",
                "type": "Binary",
                "extra": {"ID": "golusoris-mcp"},
            },
        ])

        def fake_runner(path: str) -> tuple[int, str]:
            raise subprocess.TimeoutExpired(cmd=[path, "--version"], timeout=30)

        code, _, err = self.run_smoke(
            ["--version", "v1.2.3", "--target", "linux/amd64", "--dist", str(self.dist_dir)],
            runner=fake_runner,
        )
        self.assertEqual(code, 1)
        self.assertIn("--version failed", err)

    def test_artifacts_boundary_1024_ok(self) -> None:
        entries: list[dict[str, object]] = [
            {
                "name": "golusoris",
                "path": "dist/golusoris_linux_amd64_v1/golusoris",
                "goos": "linux",
                "goarch": "amd64",
                "type": "Binary",
                "extra": {"ID": "golusoris"},
            },
            {
                "name": "golusoris-mcp",
                "path": "dist/golusoris-mcp_linux_amd64_v1/golusoris-mcp",
                "goos": "linux",
                "goarch": "amd64",
                "type": "Binary",
                "extra": {"ID": "golusoris-mcp"},
            },
        ]
        entries.extend([
            {
                "name": f"archive-{idx}",
                "path": f"dist/archive-{idx}.tar.gz",
                "goos": "linux",
                "goarch": "amd64",
                "type": "Archive",
                "extra": {"ID": "archive"},
            }
            for idx in range(1022)
        ])
        self.assertEqual(len(entries), 1024)
        self.write_artifacts(entries)

        def fake_runner(path: str) -> tuple[int, str]:
            name = "golusoris-mcp" if "golusoris-mcp" in path else "golusoris"
            return 0, f"{name} version 1.2.3"

        code, out, err = self.run_smoke(
            ["--version", "v1.2.3", "--target", "linux/amd64", "--dist", str(self.dist_dir)],
            runner=fake_runner,
        )
        self.assertEqual(code, 0)
        self.assertIn("release-smoke: golusoris version 1.2.3\n", out)
        self.assertIn("release-smoke: golusoris-mcp version 1.2.3\n", out)
        self.assertEqual(err, "")

    def test_artifacts_boundary_1025_refused(self) -> None:
        entries = [
            {
                "name": f"item-{idx}",
                "path": f"dist/item-{idx}",
                "goos": "linux",
                "goarch": "amd64",
                "type": "Binary",
                "extra": {"ID": "item"},
            }
            for idx in range(1025)
        ]
        self.assertEqual(len(entries), 1025)
        self.write_artifacts(entries)
        code, _, err = self.run_smoke(
            ["--version", "v1.2.3", "--target", "linux/amd64", "--dist", str(self.dist_dir)],
        )
        self.assertEqual(code, 1)
        self.assertIn("exceeds 1024 entries", err)

    def test_artifacts_exceeds_4mib(self) -> None:
        artifacts_path = self.dist_dir / "artifacts.json"
        artifacts_path.write_text(
            "[\n" + "  {\"name\": \"pad\"},\n" * 250000 + "  {\"name\": \"end\"}\n]\n",
            encoding="utf-8",
        )
        self.assertGreater(artifacts_path.stat().st_size, 4 * 1024 * 1024)
        code, _, err = self.run_smoke(
            ["--version", "v1.2.3", "--target", "linux/amd64", "--dist", str(self.dist_dir)],
        )
        self.assertEqual(code, 1)
        self.assertIn("exceeds 4 MiB", err)

    def test_goarch_mismatch_fails(self) -> None:
        self.write_artifacts([
            {
                "name": "golusoris",
                "path": "dist/golusoris_darwin_arm64_v1/golusoris",
                "goos": "darwin",
                "goarch": "arm64",
                "type": "Binary",
                "extra": {"ID": "golusoris"},
            },
            {
                "name": "golusoris-mcp",
                "path": "dist/golusoris-mcp_darwin_arm64_v1/golusoris-mcp",
                "goos": "darwin",
                "goarch": "arm64",
                "type": "Binary",
                "extra": {"ID": "golusoris-mcp"},
            },
        ])
        code, _, err = self.run_smoke(
            ["--version", "v1.2.3", "--target", "darwin/amd64", "--dist", str(self.dist_dir)],
        )
        self.assertEqual(code, 1)
        self.assertIn("golusoris: expected one darwin/amd64 binary, found 0", err)

    def test_real_subprocess(self) -> None:
        stub_file = self.suite_root / "fake_binary.py"
        stub_file.write_text(
            "import sys\n"
            "if '--version' in sys.argv:\n"
            "    print('golusoris version 1.2.3')\n"
            "    sys.exit(0)\n"
            "sys.exit(1)\n",
            encoding="utf-8",
        )
        code, output = release_smoke.run_binary(str(stub_file), argv_prefix=(sys.executable,))
        self.assertEqual(code, 0)
        self.assertEqual(output.strip(), "golusoris version 1.2.3")


if __name__ == "__main__":
    main()
