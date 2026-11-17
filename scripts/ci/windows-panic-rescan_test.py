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
if [[ -n "${STUB_FAIL_TIMES:-}" ]]; then
  count=0
  [[ ! -f "$STUB_COUNTER" ]] || read -r count < "$STUB_COUNTER"
  count=$((count + 1))
  printf '%s\\n' "$count" > "$STUB_COUNTER"
  if (( count <= STUB_FAIL_TIMES )); then
    echo "$STUB_FAIL_MSG" >&2
    exit 1
  fi
fi
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

    def run_scan(
        self, log: str, *args: str, state: str = "CLOSED", fail: bool = False,
        fail_times: int = 0, fail_msg: str = "stub failure", retry_delay: str = "0",
    ) -> tuple[int, str]:
        self.log.write_text(log, encoding="utf-8")
        env = {
            "PATH": f"{self.root}{os.pathsep}{os.environ['PATH']}",
            "STUB_CALLS": str(self.calls),
            "STUB_LOG": str(self.log),
            "STUB_STATE": state,
            "STUB_COUNTER": str(self.root / "counter"),
            "RESCAN_RETRY_DELAY_SECONDS": retry_delay,
            "GITHUB_REPOSITORY": "golusoris/golusoris",
        }
        if fail:
            env["STUB_FAIL"] = "1"
        if fail_times:
            env["STUB_FAIL_TIMES"] = str(fail_times)
            env["STUB_FAIL_MSG"] = fail_msg
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

    def test_transient_502_retries_then_succeeds(self) -> None:
        rc, out = self.run_scan("", fail_times=2, fail_msg="gh: Server Error (HTTP 502)")
        self.assertEqual(rc, 0, out)
        calls = self.calls_text().splitlines()
        self.assertEqual(calls.count(calls[0]), 3)
        self.assertIn("warning: attempt 1/3: gh api --paginate", out)
        self.assertIn("warning: attempt 2/3: gh api --paginate", out)
        self.assertEqual(out.count("warning:"), 2)

    def test_persistent_502_fails_after_three_calls(self) -> None:
        rc, out = self.run_scan("", fail_times=10, fail_msg="gh: Server Error (HTTP 502)")
        self.assertEqual(rc, 2, out)
        self.assertEqual(len(self.calls_text().splitlines()), 3)
        self.assertEqual(out.count("warning:"), 2)
        self.assertIn("windows-panic-rescan: error: gh api --paginate", out)

    def test_non_transient_404_is_not_retried(self) -> None:
        rc, out = self.run_scan("", fail_times=10, fail_msg="gh: Not Found (HTTP 404)")
        self.assertEqual(rc, 2, out)
        self.assertEqual(len(self.calls_text().splitlines()), 1)
        self.assertNotIn("warning:", out)

    def test_non_transient_stub_failure_is_not_retried(self) -> None:
        rc, out = self.run_scan("", fail=True)
        self.assertEqual(rc, 2, out)
        self.assertEqual(len(self.calls_text().splitlines()), 1)
        self.assertNotIn("warning:", out)

    def test_transient_429_retries_then_succeeds(self) -> None:
        rc, out = self.run_scan("", fail_times=1, fail_msg="gh: Too Many Requests (HTTP 429)")
        self.assertEqual(rc, 0, out)
        calls = self.calls_text().splitlines()
        self.assertEqual(calls.count(calls[0]), 2)
        self.assertEqual(out.count("warning:"), 1)

    def test_invalid_retry_delay_fails_closed(self) -> None:
        for delay in ("61", "x"):
            with self.subTest(delay=delay):
                rc, out = self.run_scan("", retry_delay=delay)
                self.assertEqual(rc, 2, out)
                self.assertIn("RESCAN_RETRY_DELAY_SECONDS", out)
                self.assertEqual(self.calls_text(), "")

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
