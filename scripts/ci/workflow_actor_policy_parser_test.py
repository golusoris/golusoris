#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Workflow actor policy parser boundary regression tests."""

from __future__ import annotations

import ast
import sys
import unittest
from pathlib import Path
from unittest.mock import patch

import workflow_actor_policy as policy

_FIXTURE_NAME = ".github/workflows/security-scan.yml"
_FIXTURE_PREFIX = "on:\n  push:\njobs:\n  build:\n"


class WorkflowActorPolicyParserTest(unittest.TestCase):
    def test_duplicate_key_diagnostic_preserved(self) -> None:
        findings = policy.check(_FIXTURE_NAME, "on: push\non: pull_request\njobs: {}\n")
        self.assertEqual(len(findings), 1)
        self.assertIn('workflow parser: duplicate key "on" at line 2', findings[0])
        self.assertNotIn("KeyError", findings[0])

    def test_parser_response_byte_bound(self) -> None:
        command = [
            sys.executable,
            "-c",
            f"import sys; sys.stdout.buffer.write(b'x' * {policy.MAX_RESPONSE_BYTES + 1})",
        ]
        original_reader = policy._read_parser_output
        bytes_read = 0

        def bounded_reader(process):
            original_read = process.stdout.read

            def read(size):
                nonlocal bytes_read
                self.assertLessEqual(size, policy.MAX_RESPONSE_BYTES + 1 - bytes_read)
                chunk = original_read(size)
                bytes_read += len(chunk)
                return chunk

            with patch.object(process.stdout, "read", side_effect=read):
                return original_reader(process)

        with (
            patch.object(policy, "_parser_command", return_value=command),
            patch.object(policy, "_read_parser_output", side_effect=bounded_reader),
            self.assertRaisesRegex(ValueError, "response byte bound exceeded"),
        ):
            policy._parse_document("on: push\n")
        self.assertEqual(bytes_read, policy.MAX_RESPONSE_BYTES + 1)
        with patch.object(policy, "_parser_command", return_value=command):
            findings = policy.check(_FIXTURE_NAME, "on: push\njobs: {}\n")
        self.assertTrue(any("workflow parser unavailable" in item for item in findings))

    def test_parser_response_normal_workflow(self) -> None:
        document = policy._parse_document(_FIXTURE_PREFIX + "    steps: []\n")
        self.assertEqual(document["error"], "")
        self.assertTrue(
            any(item["path"] == ["jobs", "build"] for item in document["scalars"])
        )

    def test_parser_response_nonzero_exit(self) -> None:
        command = [sys.executable, "-c", "raise SystemExit(7)"]
        with (
            patch.object(policy, "_parser_command", return_value=command),
            self.assertRaises(policy.subprocess.CalledProcessError) as raised,
        ):
            policy._parse_document("on: push\n")
        self.assertEqual(raised.exception.returncode, 7)

    def test_parser_response_timeout(self) -> None:
        command = [sys.executable, "-c", "import time; time.sleep(60)"]
        with (
            patch.object(policy, "_parser_command", return_value=command),
            patch.object(policy.time, "monotonic", side_effect=[0, 31]),
            self.assertRaises(policy.subprocess.TimeoutExpired) as raised,
        ):
            policy._parse_document("on: push\n")
        self.assertEqual(raised.exception.timeout, 30)

    def test_parser_unavailable_fails_closed(self) -> None:
        with patch.object(policy, "_parser_command", side_effect=OSError, create=True):
            findings = policy.check(_FIXTURE_NAME, "on: push\njobs: {}\n")
        self.assertTrue(any("workflow parser unavailable" in item for item in findings))

    def test_parser_output_wrong_shape_fails_closed(self) -> None:
        for document in (
            None,
            [],
            {},
            {"scalars": []},
            {"error": ""},
            {"scalars": [{}], "error": ""},
            {"scalars": [None], "error": ""},
            {"scalars": 1, "error": ""},
        ):
            with (
                self.subTest(document=document),
                patch.object(
                    policy, "_parse_document", return_value=document, create=True
                ),
            ):
                findings = policy.check(_FIXTURE_NAME, "on: push\njobs: {}\n")
                self.assertTrue(
                    any("workflow parser unavailable" in item for item in findings)
                )

    def test_suite_failure_exit_code(self) -> None:
        for filename in (
            "workflow_actor_policy_test.py",
            "workflow_actor_policy_uses_test.py",
            "workflow_actor_policy_scan_test.py",
            "workflow_actor_policy_parser_test.py",
        ):
            tree = ast.parse(
                Path(__file__).with_name(filename).read_text(encoding="utf-8")
            )
            exit_call = tree.body[-1].body[0].value
            self.assertEqual(exit_call.args[0].orelse.value, 3, filename)

    def test_expression_reference_bound_fails_closed(self) -> None:
        text = (
            _FIXTURE_PREFIX
            + "    steps:\n      - run: echo "
            + "${{ github.ref }} " * (policy.MAX_WORKFLOW_LINES + 1)
            + "${{ github.* }}\n"
        )
        self.assertTrue(policy.check(_FIXTURE_NAME, text))

    def test_duplicate_keys_fail_closed(self) -> None:
        self.assertTrue(
            policy.check(_FIXTURE_NAME, "on: push\non: pull_request\njobs: {}\n")
        )

    def test_recursive_alias_fails_closed(self) -> None:
        self.assertTrue(policy.check(_FIXTURE_NAME, "x: &loop {child: *loop}\n"))

    def test_multiple_documents_fail_closed(self) -> None:
        self.assertTrue(
            policy.check(_FIXTURE_NAME, "on: push\n---\non: pull_request\n")
        )


if __name__ == "__main__":
    sys.exit(0 if unittest.main(exit=False).result.wasSuccessful() else 3)
