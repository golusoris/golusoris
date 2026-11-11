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
    on: str = "on:\n  pull_request:\n    types: [opened, synchronize, reopened, ready_for_review]",
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


def unreadable_types_message(stripped: str) -> str:
    """Build the expected diagnostic fixture for an unreadable types form."""
    return (
        f"pull_request types form not readable: {stripped};"
        " use a one-line flow list or a block sequence"
    )


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

    def test_yaml_anchor_steps_are_gated(self) -> None:
        """Positive: steps with a YAML anchor are parsed correctly."""
        text = "\n".join([
            "on:",
            "  pull_request:",
            "    types: [opened, synchronize, reopened, ready_for_review]",
            "jobs:",
            "  build:",
            "    runs-on: ubuntu-26.04",
            "    steps: &build-steps",
            f"      - uses: {GATE}",
            "      - run: make test",
            "",
        ])
        self.assertEqual(self.check(text), [])

    def test_yaml_alias_steps_resolve_to_anchor(self) -> None:
        """Positive: a job whose steps are a YAML alias inherits the anchor's gate."""
        text = "\n".join([
            "on:",
            "  pull_request:",
            "    types: [opened, synchronize, reopened, ready_for_review]",
            "jobs:",
            "  build:",
            "    runs-on: ubuntu-26.04",
            "    steps: &build-steps",
            f"      - uses: {GATE}",
            "      - run: make test",
            "  advisory:",
            "    runs-on: windows-11-arm",
            "    continue-on-error: true",
            "    steps: *build-steps",
            "",
        ])
        self.assertEqual(self.check(text), [])

    def test_yaml_alias_without_gate_still_fails(self) -> None:
        """Negative: an alias to ungated steps does not pass."""
        text = "\n".join([
            "on:",
            "  pull_request:",
            "jobs:",
            "  build:",
            "    runs-on: ubuntu-26.04",
            "    steps: &build-steps",
            "      - uses: actions/checkout@v7",
            "      - run: make test",
            "  advisory:",
            "    runs-on: windows-11-arm",
            "    steps: *build-steps",
            "",
        ])
        self.assertEqual(len(self.check(text)), 2)

    def test_flow_types_with_ready_for_review_passes(self) -> None:
        """Positive: flow types with ready_for_review and a gated job pass."""
        on = "on:\n  pull_request:\n    types: [opened, synchronize, reopened, ready_for_review]"
        self.assertEqual(self.check(workflow(on=on, uses=GATE)), [])

    def test_block_types_with_ready_for_review_passes(self) -> None:
        """Positive: block sequence types with ready_for_review and a gated job pass."""
        on = (
            "on:\n"
            "  pull_request:\n"
            "    types:\n"
            "      - opened\n"
            "      - synchronize\n"
            "      - reopened\n"
            "      - ready_for_review"
        )
        self.assertEqual(self.check(workflow(on=on, uses=GATE)), [])

    def test_missing_types_with_gated_job_fails(self) -> None:
        """Negative: pull_request without types and a gated job reports ready_for_review violation."""
        messages = self.check(workflow(on="on:\n  pull_request:", uses=GATE))
        self.assertEqual(len(messages), 1)
        self.assertIn("ready_for_review", messages[0])

    def test_inline_pr_trigger_with_gated_job_fails(self) -> None:
        """Negative: inline on: [pull_request] with a gated job reports violation."""
        messages = self.check(workflow(on="on: [pull_request]", uses=GATE))
        self.assertEqual(len(messages), 1)
        self.assertIn("ready_for_review", messages[0])

    def test_explicit_types_without_ready_for_review_fails(self) -> None:
        """Boundary: explicit types without ready_for_review reports violation."""
        on = "on:\n  pull_request:\n    types: [opened, synchronize]"
        messages = self.check(workflow(on=on, uses=GATE))
        self.assertEqual(len(messages), 1)
        self.assertIn("ready_for_review", messages[0])

    def test_no_gated_job_without_types_passes(self) -> None:
        """Boundary: all jobs exempt and no types reports no ready_for_review violation."""
        text = "\n".join([
            "on:",
            "  pull_request:",
            "jobs:",
            "  check:",
            "    runs-on: ubuntu-26.04",
            "    if: github.event_name != 'pull_request'",
            "    steps:",
            "      - run: make test",
            "",
        ])
        self.assertEqual(self.check(text), [])

    def test_flow_mapping_types_before_other_keys_passes(self) -> None:
        """Boundary: a flow mapping lists types before another flow list."""
        on = (
            "on:\n"
            "  pull_request: {types: [opened, synchronize, reopened, ready_for_review], branches: [main]}"
        )
        self.assertEqual(self.check(workflow(on=on, uses=GATE)), [])

    def test_comment_on_pull_request_line_is_not_types(self) -> None:
        """Negative: a comment holding types on the pull_request line is ignored."""
        on = "on:\n  pull_request:  # types: [ready_for_review]"
        messages = self.check(workflow(on=on, uses=GATE))
        self.assertEqual(len(messages), 1)
        self.assertIn(
            "missing: opened, synchronize, reopened, ready_for_review", messages[0]
        )

    def test_comment_line_in_block_is_not_types(self) -> None:
        """Negative: a comment line inside the pull_request block is ignored."""
        on = "on:\n  pull_request:\n    # types: [opened, synchronize, reopened, ready_for_review]"
        messages = self.check(workflow(on=on, uses=GATE))
        self.assertEqual(len(messages), 1)
        self.assertIn(
            "missing: opened, synchronize, reopened, ready_for_review", messages[0]
        )

    def test_flow_types_with_trailing_comment_passes(self) -> None:
        """Positive: a real flow list with a trailing comment passes."""
        on = (
            "on:\n"
            "  pull_request:\n"
            "    types: [opened, synchronize, reopened, ready_for_review]  # rerun on ready"
        )
        self.assertEqual(self.check(workflow(on=on, uses=GATE)), [])

    def test_types_only_ready_for_review_fails(self) -> None:
        """Negative: types listing only ready_for_review names the other three as missing."""
        on = "on:\n  pull_request:\n    types: [ready_for_review]"
        messages = self.check(workflow(on=on, uses=GATE))
        self.assertEqual(len(messages), 1)
        self.assertIn("missing: opened, synchronize, reopened", messages[0])

    def test_types_missing_synchronize_fails(self) -> None:
        """Boundary: types missing only synchronize names exactly synchronize."""
        on = "on:\n  pull_request:\n    types: [opened, reopened, ready_for_review]"
        messages = self.check(workflow(on=on, uses=GATE))
        self.assertEqual(len(messages), 1)
        self.assertTrue(messages[0].endswith("missing: synchronize"))

    def test_exempt_gated_job_needs_no_types(self) -> None:
        """Boundary: when the only gated job is exempt, types are not required."""
        text = "\n".join([
            "on:",
            "  pull_request:",
            "jobs:",
            "  merge-group:",
            "    runs-on: ubuntu-26.04",
            "    if: github.event_name == 'merge_group'",
            "    steps:",
            f"      - uses: {GATE}",
            "",
        ])
        self.assertEqual(self.check(text), [])

    def test_inline_on_mapping_with_required_types_passes(self) -> None:
        """Positive: inline on: mapping with required types passes."""
        on = (
            "on: {pull_request: {types: [opened, synchronize, reopened, ready_for_review]},"
            " push: {branches: [main]}}"
        )
        self.assertEqual(self.check(workflow(on=on, uses=GATE)), [])

    def test_inline_on_mapping_without_types_fails(self) -> None:
        """Negative: inline on: mapping without types reports all four missing."""
        on = "on: {pull_request: {branches: [main]}}"
        messages = self.check(workflow(on=on, uses=GATE))
        self.assertEqual(len(messages), 1)
        self.assertIn(
            "missing: opened, synchronize, reopened, ready_for_review", messages[0]
        )

    def test_inline_on_mapping_null_pull_request_fails(self) -> None:
        """Boundary: a flow mapping key with no value is still a pull_request trigger."""
        messages = self.check(workflow(on="on: {pull_request, push}", uses=GATE))
        self.assertEqual(len(messages), 1)
        self.assertIn(
            "missing: opened, synchronize, reopened, ready_for_review", messages[0]
        )

    def test_unreadable_types_forms_fail(self) -> None:
        """Negative: multiline flow, anchor, alias and folded types report unreadable form."""
        cases = (
            (
                "types: [opened,",
                "on:\n  pull_request:\n    types: [opened,\n      synchronize]",
            ),
            ("types: &t [opened]", "on:\n  pull_request:\n    types: &t [opened]"),
            ("types: *t", "on:\n  pull_request:\n    types: *t"),
            ("types: >", "on:\n  pull_request:\n    types: >\n      opened"),
        )
        for expected_stripped, on in cases:
            with self.subTest(stripped=expected_stripped):
                messages = self.check(workflow(on=on, uses=GATE))
                self.assertEqual(len(messages), 1)
                expected = unreadable_types_message(expected_stripped)
                self.assertIn(expected, messages[0])

    def test_inline_unreadable_types_forms_fail(self) -> None:
        """Negative: an inline mapping whose types value is an alias, anchor or scalar reports unreadable form."""
        cases = (
            ("types: *events", "on: {pull_request: {types: *events}}"),
            ("types: &t [opened]", "on: {pull_request: {types: &t [opened]}}"),
            (
                "types: opened",
                "on: {pull_request: {branches: [main], types: opened}}",
            ),
        )
        for expected_stripped, on in cases:
            with self.subTest(stripped=expected_stripped):
                messages = self.check(workflow(on=on, uses=GATE))
                self.assertEqual(len(messages), 1)
                expected = unreadable_types_message(expected_stripped)
                self.assertIn(expected, messages[0])


if __name__ == "__main__":
    unittest.main()
