#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Regression tests for the vendored Praetor command-policy hook."""

from __future__ import annotations

import json
import os
import subprocess
import sys
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
HOOK = REPO_ROOT / ".config/agent/hooks/block_evasion.py"
MARKER = b"PRAETOR_COMMAND_POLICY_OK\n"


class BlockEvasionHookTest(unittest.TestCase):
    def run_hook(
        self,
        *,
        payload: bytes | None = None,
        arguments: tuple[str, ...] = (),
        environment: dict[str, str] | None = None,
    ) -> subprocess.CompletedProcess[bytes]:
        child_environment = {"PATH": os.environ.get("PATH", "")}
        if environment:
            child_environment.update(environment)
        return subprocess.run(  # noqa: S603 - fixed interpreter and repository script.
            [sys.executable, "-B", str(HOOK), *arguments],
            input=payload,
            capture_output=True,
            env=child_environment,
            check=False,
            timeout=10,
        )

    @staticmethod
    def payload(command: str) -> bytes:
        return json.dumps({"tool_input": {"command": command}}).encode()

    def test_json_allow_returns_exact_protocol_marker(self) -> None:
        result = self.run_hook(payload=self.payload("go test ./..."))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, MARKER)

    def test_evasion_and_topology_commands_fail_closed(self) -> None:
        commands = (
            "git commit --no-verify -m bypass",
            "git config core.hooksPath=/dev/null",
            "praetorctl adopt --path /home/kilian/dev/cordanaLLM",
        )
        for command in commands:
            with self.subTest(command=command):
                result = self.run_hook(payload=self.payload(command))
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout, b"")

    def test_invalid_and_oversized_payloads_fail_closed(self) -> None:
        payloads = (
            b"not-json",
            b"{}",
            b"x" * ((1 << 20) + 1),
        )
        for payload in payloads:
            with self.subTest(size=len(payload)):
                result = self.run_hook(payload=payload)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout, b"")

    def test_hook_exclusion_environment_fails_closed(self) -> None:
        for variable in ("LEFTHOOK=0", "LEFTHOOK_EXCLUDE=lint", "LEFTHOOK_SKIP=pre-push"):
            key, value = variable.split("=", 1)
            with self.subTest(variable=variable):
                result = self.run_hook(arguments=("--environment",), environment={key: value})
                self.assertNotEqual(result.returncode, 0)

    def test_benign_argv_is_allowed_without_json_marker(self) -> None:
        result = self.run_hook(arguments=("git", "status"))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, b"")


if __name__ == "__main__":
    unittest.main()
