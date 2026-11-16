#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Workflow actor policy scan regression tests."""

from __future__ import annotations

import sys
import tempfile
import unittest
from pathlib import Path

import workflow_actor_policy as policy


class WorkflowActorPolicyScanTest(unittest.TestCase):
    def test_allowed_entries_are_pull_request_only(self) -> None:
        self.assertEqual(len(policy.ALLOWED), 3)
        for (path, job, expr), reason in policy.ALLOWED.items():
            self.assertTrue(path and job and reason.strip())
            self.assertTrue(
                expr.endswith("&& github.event_name == 'pull_request'"),
                f"{path}:{job}: {expr}",
            )

    def test_all_allowances_are_hit_by_repository_scan(self) -> None:
        self.assertEqual(len(policy.ALLOWED_LINES), 21)
        for (path, job, line_text), reason in list(policy.ALLOWED_LINES.items())[
            : policy.MAX_WORKFLOW_LINES
        ]:
            self.assertTrue(path and job and line_text and reason.strip())
        findings, hit_ifs, hit_lines = policy.scan_with_hits(policy.REPO_ROOT)
        self.assertEqual(findings, [])
        self.assertEqual(hit_ifs, set(policy.ALLOWED))
        self.assertEqual(hit_lines, set(policy.ALLOWED_LINES))

    def test_scan_with_hits_omits_unhit_allowances(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            workflows = root / ".github/workflows"
            workflows.mkdir(parents=True)
            (workflows / "ok.yml").write_text(
                "on:\n  push:\njobs:\n  ok:\n    runs-on: ubuntu-26.04\n",
                encoding="utf-8",
            )
            findings, hit_ifs, hit_lines = policy.scan_with_hits(root)
            self.assertEqual(findings, [])
            self.assertEqual(hit_ifs, set())
            self.assertEqual(hit_lines, set())

    def test_scan_checks_github_and_gitea_workflows(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / ".github/workflows").mkdir(parents=True)
            (root / ".gitea/workflows").mkdir(parents=True)
            (root / ".github/workflows/a.yaml").write_text(
                "jobs:\n  a:\n    if: sender.login != 'bot'\n",
                encoding="utf-8",
            )
            (root / ".gitea/workflows/b.yml").write_text(
                "jobs:\n  b:\n    if: user.login != 'bot'\n",
                encoding="utf-8",
            )
            findings = policy.scan(root)
            self.assertEqual(len(findings), 2)
            self.assertIn(".github/workflows/a.yaml:3:a:", findings[0])
            self.assertIn(".gitea/workflows/b.yml:3:b:", findings[1])

    def test_scan_invalid_utf8_workflow_reports_unreadable_finding(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            workflows = root / ".github/workflows"
            workflows.mkdir(parents=True)
            (workflows / "broken.yml").write_bytes(b"name: \xff\xfe\n")
            findings = policy.scan(root)
            self.assertEqual(len(findings), 1)
            self.assertEqual(
                findings[0],
                ".github/workflows/broken.yml:1:<top>: "
                "unreadable workflow: UnicodeDecodeError",
            )

    def test_scan_oserror_workflow_reports_unreadable_finding(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            workflows = root / ".github/workflows"
            (workflows / "dir.yaml").mkdir(parents=True)
            findings = policy.scan(root)
            self.assertEqual(len(findings), 1)
            self.assertTrue(
                findings[0].startswith(
                    ".github/workflows/dir.yaml:1:<top>: unreadable workflow: "
                ),
                findings[0],
            )

    def test_repository_workflows_comply(self) -> None:
        self.assertEqual(policy.scan(policy.REPO_ROOT), [])


if __name__ == "__main__":
    sys.exit(0 if unittest.main(exit=False).result.wasSuccessful() else 3)
