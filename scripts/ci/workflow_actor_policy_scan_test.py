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

    def test_folded_block_multiline_safe_expression_passes(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            policy.write_fixture(
                root,
                ".github/workflows/praetor.yml",
                "on:\n  pull_request:\n"
                "concurrency:\n  group: >-\n"
                "    ${{ github.workflow }}-${{ github.event.pull_request.number ||\n"
                "    github.run_id }}\n"
                "  cancel-in-progress: ${{ github.event_name == 'pull_request' }}\n"
                "jobs: {}\n",
            )
            self.assertEqual(policy.scan(root), [])

    def test_folded_block_multiline_split_github_actor_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            policy.write_fixture(
                root,
                ".github/workflows/actor.yml",
                "on:\n  pull_request:\n"
                "concurrency:\n  group: >-\n"
                "    ${{ github.\n"
                "    actor }}\n"
                "jobs: {}\n",
            )
            self.assertEqual(
                policy.scan(root),
                [
                    (
                        ".github/workflows/actor.yml:5:<top>: "
                        "disallowed actor reference: ${{ github. actor }}"
                    )
                ],
            )

    def test_folded_block_multiline_split_triggering_actor_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            policy.write_fixture(
                root,
                ".github/workflows/triggering.yml",
                "on:\n  pull_request:\n"
                "concurrency:\n  group: >-\n"
                "    ${{ github.triggering_\n"
                "    actor }}\n"
                "jobs: {}\n",
            )
            self.assertEqual(
                policy.scan(root),
                [
                    (
                        ".github/workflows/triggering.yml:5:<top>: "
                        "disallowed actor reference: ${{ github.triggering_ actor }}"
                    )
                ],
            )

    def test_plain_scalar_multiline_expression_refused(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            policy.write_fixture(
                root,
                ".github/workflows/plain.yml",
                "on:\n  pull_request:\n"
                "concurrency:\n"
                "  group: ${{ github.workflow }}-${{ github.event.pull_request.number ||\n"
                "    github.run_id }}\n"
                "jobs: {}\n",
            )
            self.assertEqual(
                policy.scan(root),
                [
                    (
                        ".github/workflows/plain.yml:4:<top>: "
                        "expression spans lines refused"
                    )
                ],
            )

    def test_block_scalar_unclosed_expression_span_refused(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            policy.write_fixture(
                root,
                ".github/workflows/unclosed.yml",
                "on:\n  pull_request:\n"
                "concurrency:\n  group: >-\n"
                "    ${{ github.workflow }}-${{ github.event.pull_request.number ||\n"
                "  cancel-in-progress: ${{ github.event_name == 'pull_request' }}\n"
                "jobs: {}\n",
            )
            self.assertEqual(
                policy.scan(root),
                [
                    (
                        ".github/workflows/unclosed.yml:5:<top>: "
                        "expression spans lines refused"
                    )
                ],
            )

    def test_block_scalar_expression_span_over_eight_lines_refused(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            policy.write_fixture(
                root,
                ".github/workflows/long-span.yml",
                "on:\n  pull_request:\n"
                "concurrency:\n  group: >-\n"
                "    ${{ github.workflow ||\n"
                "    github.run_id ||\n"
                "    github.run_number ||\n"
                "    github.run_attempt ||\n"
                "    github.job ||\n"
                "    github.ref ||\n"
                "    github.ref_name ||\n"
                "    github.sha ||\n"
                "    github.workspace }}\n"
                "jobs: {}\n",
            )
            self.assertEqual(
                policy.scan(root),
                [
                    (
                        ".github/workflows/long-span.yml:5:<top>: "
                        "expression spans lines refused"
                    )
                ],
            )


if __name__ == "__main__":
    sys.exit(0 if unittest.main(exit=False).result.wasSuccessful() else 3)
