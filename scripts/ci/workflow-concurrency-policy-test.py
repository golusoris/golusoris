#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Every pull-request workflow cancels its superseded runs, and only those."""

from __future__ import annotations

import datetime
import re
import sys
import tempfile
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
# Gitea runs on its own runner without this queue; its ci.yml keeps a ref-wide group.
WORKFLOW_DIRS: tuple[str, ...] = (".github/workflows",)
PR_EVENTS: tuple[str, ...] = ("pull_request", "pull_request_target")

_ENGINE_REASON = (
    "C: emitted by Praetor; the engine template gains the group "
    "(order engine-emitted-workflow-concurrency-1936)"
)

# path -> (reason, expiry). An expired entry fails like a missing group.
EXCEPTIONS: dict[str, tuple[str, str]] = {
    ".github/workflows/praetor-api.yml": (
        _ENGINE_REASON,
        "2026-10-26",
    ),
    ".github/workflows/praetor-docs.yml": (
        _ENGINE_REASON,
        "2026-10-26",
    ),
}

_TOP_KEY = re.compile(r"^(?P<q>['\"]?)(?P<key>[A-Za-z_-]+)(?P=q):(?P<rest>.*)$")
_CHILD_KEY = re.compile(r"^  (?P<key>[A-Za-z_-]+):(?P<rest>.*)$")


def _top_blocks(text: str) -> dict[str, tuple[str, list[str]]]:
    """Map each top-level key to its inline value and indented body lines."""
    blocks: dict[str, tuple[str, list[str]]] = {}
    current = ""
    for line in text.splitlines():
        match = _TOP_KEY.match(line)
        if match:
            current = match.group("key")
            blocks[current] = (match.group("rest").strip(), [])
        elif current and (line.startswith(" ") or not line.strip()):
            blocks[current][1].append(line)
        elif line.strip() and not line.startswith("#"):
            current = ""
    return blocks


def _events(blocks: dict[str, tuple[str, list[str]]]) -> set[str]:
    inline, body = blocks.get("on", ("", []))
    events = set(re.findall(r"[A-Za-z_]+", inline))
    for line in body:
        match = _CHILD_KEY.match(line)
        if match:
            events.add(match.group("key"))
    return events


def _exception_findings(name: str, today: datetime.date) -> list[str] | None:
    if name not in EXCEPTIONS:
        return None
    reason, expiry = EXCEPTIONS[name]
    if today > datetime.date.fromisoformat(expiry):
        return [f"{name}: exception expired {expiry} ({reason})"]
    return []


def _concurrency_fields(lines: list[str]) -> dict[str, str]:
    fields: dict[str, str] = {}
    for line in lines:
        match = _CHILD_KEY.match(line)
        if match:
            fields[match.group("key")] = match.group("rest").strip()
    return fields


def _group_findings(name: str, group: str, events: set[str]) -> list[str]:
    findings: list[str] = []
    if group.startswith("${{") and "github.workflow" not in group:
        findings.append(
            f"{name}: concurrency group has neither github.workflow nor a literal prefix",
        )
    if "pull_request_target" in events:
        if "github.event.pull_request.number" not in group:
            findings.append(
                f"{name}: pull_request_target runs share github.ref with the base "
                "branch; key the group by github.event.pull_request.number",
            )
    elif "github.ref" not in group and "github.event.pull_request.number" not in group:
        findings.append(f"{name}: concurrency group is not keyed by PR number or ref")
    return findings


def _cancel_findings(name: str, cancel: str) -> list[str]:
    findings: list[str] = []
    if "github.event_name" not in cancel:
        findings.append(
            f"{name}: cancel-in-progress must be scoped to pull-request events, "
            f"got {cancel or 'nothing'}",
        )
    return findings


def check(name: str, text: str, today: datetime.date) -> list[str]:
    """Return the policy findings for one workflow file."""
    blocks = _top_blocks(text)
    events = _events(blocks)
    if not events.intersection(PR_EVENTS):
        return []
    exceptions = _exception_findings(name, today)
    if exceptions is not None:
        return exceptions
    if "concurrency" not in blocks:
        return [f"{name}: pull-request workflow has no top-level concurrency group"]
    fields = _concurrency_fields(blocks["concurrency"][1])
    return _group_findings(name, fields.get("group", ""), events) + _cancel_findings(
        name, fields.get("cancel-in-progress", "")
    )


def scan(root: Path, today: datetime.date) -> list[str]:
    findings: list[str] = []
    for directory in WORKFLOW_DIRS:
        for path in sorted((root / directory).glob("*.y*ml")):
            name = path.relative_to(root).as_posix()
            findings.extend(check(name, path.read_text(encoding="utf-8"), today))
    return findings


_GOOD = """name: x
'on':
  pull_request:
  push:
    branches: [main]
concurrency:
  group: ${{ github.workflow }}-${{ github.event.pull_request.number || github.ref }}
  cancel-in-progress: ${{ github.event_name == 'pull_request' }}
jobs: {}
"""
_TODAY = datetime.date(2026, 10, 16)


class WorkflowConcurrencyPolicyTest(unittest.TestCase):
    def test_scoped_group_passes(self) -> None:
        self.assertEqual(check("w.yml", _GOOD, _TODAY), [])

    def test_missing_group_fails(self) -> None:
        text = _GOOD.split("concurrency:")[0] + "jobs: {}\n"
        self.assertIn(
            "no top-level concurrency", " ".join(check("w.yml", text, _TODAY))
        )

    def test_unconditional_cancel_fails(self) -> None:
        text = _GOOD.replace(
            "${{ github.event_name == 'pull_request' }}",
            "true",
        )
        self.assertIn(
            "scoped to pull-request events", " ".join(check("w.yml", text, _TODAY))
        )

    def test_pull_request_target_needs_pr_number(self) -> None:
        text = (
            "on:\n  pull_request_target:\n    types: [opened]\n"
            "concurrency:\n  group: ${{ github.workflow }}-${{ github.ref }}\n"
            "  cancel-in-progress: ${{ github.event_name == 'pull_request_target' }}\n"
        )
        self.assertIn("pull_request.number", " ".join(check("w.yml", text, _TODAY)))

    def test_inline_trigger_list_is_seen(self) -> None:
        self.assertNotEqual(
            check("w.yml", "on: [push, pull_request]\njobs: {}\n", _TODAY), []
        )

    def test_non_pr_workflow_is_ignored(self) -> None:
        self.assertEqual(
            check("w.yml", "on:\n  schedule:\n    - cron: '0 0 * * *'\n", _TODAY), []
        )

    def test_exception_expires(self) -> None:
        name = next(iter(EXCEPTIONS))
        text = "on:\n  pull_request:\n"
        self.assertEqual(check(name, text, _TODAY), [])
        late = datetime.date.fromisoformat(EXCEPTIONS[name][1]) + datetime.timedelta(
            days=1
        )
        self.assertIn("exception expired", " ".join(check(name, text, late)))

    def test_group_needs_workflow_or_prefix(self) -> None:
        bare = _GOOD.replace("${{ github.workflow }}-", "")
        self.assertIn("literal prefix", " ".join(check("w.yml", bare, _TODAY)))
        prefixed = _GOOD.replace("${{ github.workflow }}-", "x-")
        self.assertEqual(check("w.yml", prefixed, _TODAY), [])

    def test_scan_reads_workflow_files(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / ".github/workflows").mkdir(parents=True)
            (root / ".github/workflows/ci.yml").write_text(
                "on: [pull_request]\n", encoding="utf-8"
            )
            self.assertEqual(len(scan(root, _TODAY)), 1)

    def test_repository_workflows_comply(self) -> None:
        self.assertEqual(
            scan(REPO_ROOT, datetime.datetime.now(datetime.UTC).date()), []
        )


if __name__ == "__main__":
    sys.exit(0 if unittest.main(exit=False).result.wasSuccessful() else 1)
