#!/usr/bin/env python3

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Exercise the issue 800 rescan against a stub gh, without the GitHub API."""

from __future__ import annotations

import os
import subprocess
import sys
import tempfile
from pathlib import Path
from unittest import TestCase, main

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts/ci/windows-panic-rescan.py"
CRASH = (
    "panic: runtime error: invalid memory address or nil pointer dereference\n"
    "FAIL\tgithub.com/golusoris/golusoris/db/bun\t0.054s\n"
)
STUB = """#!/usr/bin/env bash
set -euo pipefail
printf '%s\\n' "$*" >> "$STUB_CALLS"
[[ -z "${STUB_FAIL:-}" ]] || { echo "stub failure" >&2; exit 1; }
case "$*" in
  *"/workflows/portability.yml/runs"*) echo 11 ;;
  *"/runs/11/jobs"*) echo '{"id":7,"html_url":"https://example.test/job/7"}' ;;
  *"/jobs/7/logs"*) cat "$STUB_LOG" ;;
  "issue view"*) echo "$STUB_STATE" ;;
  "issue reopen"*) ;;
  *) exit 97 ;;
esac
"""


class WindowsPanicRescanTest(TestCase):
    def setUp(self) -> None:
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        stub = self.root / "gh"
        stub.write_text(STUB, encoding="utf-8")
        stub.chmod(0o755)
        self.calls = self.root / "calls.log"
        self.log = self.root / "job.log"

    def run_scan(self, log: str, *args: str, state: str = "CLOSED", fail: bool = False) -> tuple[int, str]:
        self.log.write_text(log, encoding="utf-8")
        env = {
            "PATH": f"{self.root}{os.pathsep}{os.environ['PATH']}",
            "STUB_CALLS": str(self.calls),
            "STUB_LOG": str(self.log),
            "STUB_STATE": state,
            "GITHUB_REPOSITORY": "golusoris/golusoris",
        }
        if fail:
            env["STUB_FAIL"] = "1"
        out = subprocess.run(  # noqa: S603 - fixed interpreter and repository script.
            [sys.executable, "-B", str(SCRIPT), *args],
            capture_output=True, text=True, env=env, timeout=60, check=False,
        )
        return out.returncode, out.stdout + out.stderr

    def calls_text(self) -> str:
        return self.calls.read_text(encoding="utf-8") if self.calls.exists() else ""

    def test_hit_reopens_closed_issue(self) -> None:
        rc, out = self.run_scan(CRASH, "--reopen")
        self.assertEqual(rc, 1, out)
        self.assertIn("hit https://example.test/job/7", out)
        self.assertIn("issue reopen 800", self.calls_text())

    def test_hit_leaves_open_issue_alone(self) -> None:
        rc, out = self.run_scan(CRASH, "--reopen", state="OPEN")
        self.assertEqual(rc, 1, out)
        self.assertNotIn("issue reopen", self.calls_text())

    def test_dry_run_never_reopens(self) -> None:
        rc, out = self.run_scan(CRASH)
        self.assertEqual(rc, 1, out)
        self.assertNotIn("issue ", self.calls_text())

    def test_nil_panic_in_other_package_is_no_hit(self) -> None:
        log = CRASH.replace("db/bun", "db/sqlite")
        rc, out = self.run_scan(log, "--reopen")
        self.assertEqual(rc, 0, out)
        self.assertNotIn("issue reopen", self.calls_text())

    def test_bun_failure_without_nil_panic_is_no_hit(self) -> None:
        rc, out = self.run_scan("--- FAIL: TestX\nFAIL\tgithub.com/golusoris/golusoris/db/bun\t1.2s\n", "--reopen")
        self.assertEqual(rc, 0, out)

    def test_bun_subpackage_is_no_hit(self) -> None:
        rc, out = self.run_scan(CRASH.replace("db/bun\t", "db/bunx\t"))
        self.assertEqual(rc, 0, out)

    def test_gh_error_fails_closed(self) -> None:
        rc, out = self.run_scan(CRASH, "--reopen", fail=True)
        self.assertEqual(rc, 2, out)
        self.assertIn("windows-panic-rescan: error:", out)

    def test_since_is_floored_at_the_fix_merge(self) -> None:
        rc, out = self.run_scan("", "--since", "2026-10-01")
        self.assertEqual(rc, 0, out)
        self.assertIn("created=%3E%3D2026-10-15T15:20:00Z", self.calls_text())

    def test_later_since_is_kept(self) -> None:
        rc, out = self.run_scan("", "--since", "2026-11-02")
        self.assertEqual(rc, 0, out)
        self.assertIn("created=%3E%3D2026-11-02&", self.calls_text())


if __name__ == "__main__":
    main()
