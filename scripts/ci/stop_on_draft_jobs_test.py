# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Positive, negative, and boundary tests for the per-job stop-on-draft check."""

from __future__ import annotations

import contextlib
import io
import tempfile
import unittest
from pathlib import Path

import stop_on_draft_jobs

SHA = "e601567f4e1acb72525f361f80dac6d89cbad553"
GATE = f"golusoris/golusoris/.github/actions/stop-on-draft@{SHA}"


def workflow(
    on: str = "on:\n  pull_request:",
    header: str = "build:",
    cond: str = "",
    uses: str = GATE,
) -> str:
    """Render a one-job workflow."""
    lines = [on, "jobs:", f"  {header}", "    runs-on: ubuntu-26.04"]
    if cond:
        lines.append(f"    if: {cond}")
    lines += ["    steps:", f"      - uses: {uses}", "      - run: make test", ""]
    return "\n".join(lines)


class StopOnDraftJobsTest(unittest.TestCase):
    """Exercise the job checker on inline fixtures."""

    def setUp(self) -> None:
        """Give each test a fresh workflow path."""
        directory = tempfile.TemporaryDirectory(prefix="golusoris-stop-on-draft-")
        self.addCleanup(directory.cleanup)
        self.path = Path(directory.name) / "wf.yml"

    def check(self, text: str) -> list[str]:
        """Write text and return its violations."""
        self.path.write_text(text, encoding="utf-8")
        return stop_on_draft_jobs.violations(self.path)

    def test_gated_job_passes(self) -> None:
        """Positive: the pinned action as first step, with a trailing comment."""
        self.assertEqual(self.check(workflow(uses=f"{GATE} # #1212")), [])

    def test_or_disjunction_is_not_exempt(self) -> None:
        """Negative: a top-level || can still run the job on a pull request."""
        cond = "github.event_name != 'pull_request' && false || github.event_name == 'pull_request'"
        self.assertEqual(
            len(self.check(workflow(cond=cond, uses="actions/checkout@v7"))), 1
        )

    def test_grouped_or_stays_exempt(self) -> None:
        """Boundary: || inside parentheses after != pull_request && keeps the exemption."""
        cond = "github.event_name != 'pull_request' && (github.event_name == 'push' || inputs.publish)"
        self.assertEqual(
            self.check(workflow(cond=cond, uses="actions/checkout@v7")), []
        )

    def test_header_with_comment_is_a_job(self) -> None:
        """Negative: a job header with a trailing comment is still checked."""
        self.assertEqual(
            len(
                self.check(
                    workflow(header="build: # required", uses="actions/checkout@v7")
                )
            ),
            1,
        )

    def test_on_header_comment_reads_block(self) -> None:
        """Negative: a comment after on: does not hide the block below."""
        text = workflow(
            on="on: # triggers\n  pull_request:", uses="actions/checkout@v7"
        )
        self.assertEqual(len(self.check(text)), 1)

    def test_block_sequence_trigger(self) -> None:
        """Negative: on: with a - pull_request sequence item triggers the check."""
        for on in (
            "on:\n  - push\n  - pull_request",
            "on:\n- push\n- pull_request # x",
        ):
            with self.subTest(on=on):
                text = workflow(on=on, uses="actions/checkout@v7")
                self.assertEqual(len(self.check(text)), 1)

    def test_nested_pull_request_value_is_not_a_trigger(self) -> None:
        """Boundary: pull_request nested under push is a value, not a trigger."""
        for on in (
            "on: {push: {branches: [pull_request]}}",
            "on:\n  push:\n    branches:\n      - pull_request",
            "on:\n  push:\n    branches: [pull_request]",
        ):
            with self.subTest(on=on):
                text = workflow(on=on, uses="actions/checkout@v7")
                self.assertEqual(self.check(text), [])

    def test_direct_flow_entries_trigger(self) -> None:
        """Negative: direct flow entries, with or without nested values, trigger."""
        for on in (
            "on: {push: {branches: [main]}, pull_request: {}}",
            "on: [push, 'pull_request']",
            "on: pull_request",
        ):
            with self.subTest(on=on):
                text = workflow(on=on, uses="actions/checkout@v7")
                self.assertEqual(len(self.check(text)), 1)

    def test_quoted_flow_trigger(self) -> None:
        """Negative: on: ["pull_request"] still triggers the check."""
        self.assertEqual(
            len(
                self.check(
                    workflow(on='on: ["pull_request"]', uses="actions/checkout@v7")
                )
            ),
            1,
        )

    def test_quoted_block_trigger(self) -> None:
        """Negative: a quoted pull_request key still triggers the check."""
        on = "on:\n  'pull_request':"
        self.assertEqual(
            len(self.check(workflow(on=on, uses="actions/checkout@v7"))), 1
        )

    def test_foreign_action_identity(self) -> None:
        """Negative: another owner's stop-on-draft at a full SHA does not count."""
        self.assertEqual(
            len(self.check(workflow(uses=f"another-owner/stop-on-draft@{SHA}"))), 1
        )

    def test_suffixed_ref(self) -> None:
        """Negative: a SHA followed by a branch suffix is not a pinned ref."""
        self.assertEqual(len(self.check(workflow(uses=f"{GATE}-branch"))), 1)

    def test_not_pull_request_workflow(self) -> None:
        """Boundary: a workflow without pull_request needs no gate."""
        self.assertEqual(
            self.check(workflow(on="on:\n  push:", uses="actions/checkout@v7")), []
        )

    def test_usage_error_exits_three(self) -> None:
        """Negative: no arguments is an error, not a policy mismatch."""
        with contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(stop_on_draft_jobs.main([]), 3)

    def test_unreadable_workflow_exits_three(self) -> None:
        """Negative: a missing workflow fails closed with exit 3."""
        with contextlib.redirect_stderr(io.StringIO()):
            self.assertEqual(stop_on_draft_jobs.main([str(self.path)]), 3)


if __name__ == "__main__":
    unittest.main()
