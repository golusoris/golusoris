#!/usr/bin/env python3

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Reopen issue 800 when a Portability Windows log shows a db/bun nil-pointer panic.

Exit 0 = no hit, 1 = hit, 2 = scan error. Dry run unless --reopen.
"""

from __future__ import annotations

import argparse
import datetime
import json
import os
import re
import subprocess
import sys

REPO = os.environ.get("GITHUB_REPOSITORY", "golusoris/golusoris")
ISSUE = "800"
NIL = "invalid memory address or nil pointer dereference"
FAIL_BUN = re.compile(r"FAIL\s+github\.com/golusoris/golusoris/db/bun\s")
# #870 (cd0d780) merged here; earlier crashes are the known pre-fix cause.
FIX_FLOOR = "2026-10-15T15:20:00Z"
GH_TIMEOUT_SECONDS = 300


def gh(args: list[str]) -> str:
    out = subprocess.run(  # noqa: S603 - fixed gh subcommands built in this file.
        ["gh", *args],  # noqa: S607 - gh CLI on runner PATH.
        capture_output=True, text=True, timeout=GH_TIMEOUT_SECONDS, check=False,
    )
    if out.returncode != 0:
        raise RuntimeError(f"gh {' '.join(args[:3])}: rc={out.returncode}: {out.stderr.strip()[:300]}")
    return out.stdout


def failed_runs(since: str) -> list[str]:
    raw = gh([
        "api", "--paginate",
        f"repos/{REPO}/actions/workflows/portability.yml/runs?created=%3E%3D{since}&status=failure&per_page=100",
        "-q", ".workflow_runs[].id",
    ])
    return raw.split()


def windows_jobs(run_id: str) -> list[dict[str, object]]:
    raw = gh([
        "api", "--paginate", f"repos/{REPO}/actions/runs/{run_id}/jobs?filter=all&per_page=100",
        "-q", '.jobs[]|select(.conclusion=="failure" and (.name|test("Windows")))|{id,html_url}',
    ])
    return [json.loads(line) for line in raw.splitlines() if line.strip()]


def is_hit(log: str) -> bool:
    return NIL in log and FAIL_BUN.search(log) is not None


def scan(since: str, run_ids: list[str]) -> tuple[int, int, list[str]]:
    runs = run_ids or failed_runs(since)
    hits: list[str] = []
    jobs = 0
    for run in runs:
        for job in windows_jobs(run):
            jobs += 1
            if is_hit(gh(["api", "--allow-escape-sequences", f"repos/{REPO}/actions/jobs/{job['id']}/logs"])):
                hits.append(str(job["html_url"]))
    return len(runs), jobs, hits


def reopen(hits: list[str]) -> None:
    state = gh(["issue", "view", ISSUE, "-R", REPO, "--json", "state", "-q", ".state"]).strip()
    if state != "CLOSED":
        print(f"windows-panic-rescan: #{ISSUE} is {state}, not reopened")
        return
    gh(["issue", "reopen", ISSUE, "-R", REPO, "--comment",
        "Reopened by the weekly Portability rescan: db/bun nil-pointer panic in " + ", ".join(hits)])
    print(f"windows-panic-rescan: reopened #{ISSUE}")


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--since", default=(datetime.date.today() - datetime.timedelta(days=8)).isoformat())
    ap.add_argument("--run-id", action="append", default=[])
    ap.add_argument("--reopen", action="store_true")
    a = ap.parse_args(argv)
    since = max(a.since, FIX_FLOOR)
    try:
        runs, jobs, hits = scan(since, a.run_id)
        print(f"windows-panic-rescan: since={since} runs={runs} windows_failed_jobs={jobs} hits={len(hits)}")
        for url in hits:
            print(f"hit {url}")
        if hits and a.reopen:
            reopen(hits)
    except (RuntimeError, subprocess.TimeoutExpired, json.JSONDecodeError, KeyError) as err:
        print(f"windows-panic-rescan: error: {err}", file=sys.stderr)
        return 2
    return 1 if hits else 0


if __name__ == "__main__":
    sys.exit(main())
