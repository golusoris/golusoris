#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Policy tests verifying Claude Code hook commands resolve from project directory."""

from __future__ import annotations

import json
import os
import re
import subprocess
import tempfile
import unittest
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
SETTINGS_PATH = REPO_ROOT / ".claude/settings.json"
MAX_HOOK_ENTRIES = 64
HOOK_COMMAND_PATTERN = re.compile(
    r'^(?:python3 )?"\$CLAUDE_PROJECT_DIR"/(?P<rel>[^"\'\s]+)$'
)


def reject_duplicate_keys(pairs: list[tuple[str, object]]) -> dict[str, object]:
    result: dict[str, object] = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate key in settings JSON: {key!r}")
        result[key] = value
    return result


def load_settings(path: Path) -> dict[str, object]:
    with path.open("r", encoding="utf-8") as file:
        data = json.load(file, object_pairs_hook=reject_duplicate_keys)
    if not isinstance(data, dict):
        raise ValueError("settings JSON root must be an object")
    return data


def _collect_group_commands(
    event_name: str,
    group: dict[str, object],
) -> tuple[list[tuple[str, str, str]], list[str]]:
    matcher = str(group.get("matcher", ""))
    group_hooks = group.get("hooks")
    if not isinstance(group_hooks, list):
        return [], []

    entries: list[tuple[str, str, str]] = []
    problems: list[str] = []
    for hook in group_hooks[: MAX_HOOK_ENTRIES + 1]:
        if not isinstance(hook, dict) or hook.get("type") != "command":
            continue
        cmd = hook.get("command")
        if not isinstance(cmd, str):
            problems.append(f"{event_name}: command hook missing string command")
            continue
        entries.append((event_name, matcher, cmd))
    return entries, problems


def _collect_command_entries(
    data: dict[str, object],
) -> tuple[list[tuple[str, str, str]], list[str]]:
    hooks_section = data.get("hooks")
    if not isinstance(hooks_section, dict):
        return [], ["no hook commands"]

    entries: list[tuple[str, str, str]] = []
    problems: list[str] = []

    # Inspect configured event groups under the hooks mapping.
    for event_name, groups in list(hooks_section.items())[: MAX_HOOK_ENTRIES + 1]:
        if not isinstance(groups, list):
            continue
        for group in groups[: MAX_HOOK_ENTRIES + 1]:
            if not isinstance(group, dict):
                continue
            group_entries, group_problems = _collect_group_commands(
                str(event_name), group
            )
            entries.extend(group_entries)
            problems.extend(group_problems)
            if len(entries) > MAX_HOOK_ENTRIES:
                problems.append(
                    f"hook count exceeds maximum limit of {MAX_HOOK_ENTRIES}"
                )
                return entries, problems

    if not entries and not problems:
        return [], ["no hook commands"]
    return entries, problems


def _validate_hook_command(
    event_name: str,
    command: str,
    root: Path,
) -> list[str]:
    match = HOOK_COMMAND_PATTERN.fullmatch(command)
    if match is None:
        return [f"{event_name}: command does not match allowed pattern: {command}"]

    rel = match.group("rel")
    if rel.startswith("/") or ".." in Path(rel).parts:
        return [f"{event_name}: hook script path must be relative: {rel}"]

    problems: list[str] = []

    # Verify script is tracked in git index.
    res_tracked = subprocess.run(  # noqa: S603 - fixed git command on runner PATH.
        ["git", "-C", str(root), "ls-files", "--error-unmatch", "--", rel],  # noqa: S607 - git executable on runner PATH.
        capture_output=True,
        check=False,
        timeout=10,
    )
    if res_tracked.returncode != 0:
        return [f"{event_name}: hook script is not git-tracked: {rel}"]

    # Shell scripts must have executable mode 100755 in git index.
    if rel.endswith(".sh"):
        res_mode = subprocess.run(  # noqa: S603 - fixed git command on runner PATH.
            ["git", "-C", str(root), "ls-files", "-s", "--", rel],  # noqa: S607 - git executable on runner PATH.
            capture_output=True,
            check=False,
            text=True,
            timeout=10,
        )
        parts = res_mode.stdout.split()
        mode = parts[0] if parts else ""
        if mode != "100755":
            problems.append(
                f"{event_name}: hook script {rel!r} git index mode {mode!r} != 100755"
            )
    return problems


def check_settings(data: dict, root: Path) -> list[str]:
    if not isinstance(data, dict):
        return ["settings root must be a dict"]

    entries, problems = _collect_command_entries(data)
    if len(entries) > MAX_HOOK_ENTRIES or (problems and not entries):
        return problems

    # Validate each collected command against repository policy.
    for event_name, _matcher, command in entries:
        problems.extend(_validate_hook_command(event_name, command, root))

    return problems


def planted_settings(command: str) -> dict:
    return {
        "hooks": {
            "PreToolUse": [
                {
                    "matcher": "Bash",
                    "hooks": [{"type": "command", "command": command}],
                }
            ]
        }
    }


class ClaudeHooksPolicyTest(unittest.TestCase):
    def test_every_command_anchors_at_project_dir(self) -> None:
        settings = load_settings(SETTINGS_PATH)
        problems = check_settings(settings, REPO_ROOT)
        self.assertEqual(problems, [], f"settings validation failed: {problems}")

    def test_hooks_run_from_outside_checkout(self) -> None:
        settings = load_settings(SETTINGS_PATH)
        entries, _ = _collect_command_entries(settings)
        self.assertGreater(len(entries), 0, "no hook commands configured")

        with tempfile.TemporaryDirectory() as tmp_str:
            tmp_path = Path(tmp_str)
            env = {
                "PATH": os.environ["PATH"],
                "CLAUDE_PROJECT_DIR": str(REPO_ROOT),
                "HOME": str(tmp_path),
            }
            # Execute each hook from outside checkout with benign input envelope.
            for event_name, matcher, command in entries:
                if matcher == "Bash":
                    payload = {
                        "tool_name": "Bash",
                        "tool_input": {"command": "true"},
                        "cwd": str(tmp_path),
                    }
                elif "Edit" in matcher or "Write" in matcher:
                    payload = {
                        "tool_name": "Write",
                        "tool_input": {
                            "file_path": f"{tmp_path}/notes.txt",
                            "content": "x",
                        },
                        "cwd": str(tmp_path),
                    }
                else:
                    payload = {"tool_name": matcher, "cwd": str(tmp_path)}

                with self.subTest(
                    event=event_name, matcher=matcher, command=command
                ):
                    result = subprocess.run(  # noqa: S603 - fixed system shell on runner PATH.
                        ["sh", "-c", command],  # noqa: S607 - sh executable on runner PATH.
                        cwd=str(tmp_path),
                        env=env,
                        input=json.dumps(payload),
                        text=True,
                        capture_output=True,
                        timeout=30,
                    )
                    failure_msg = (
                        f"\ncommand: {command}\n"
                        f"returncode: {result.returncode}\n"
                        f"stderr: {result.stderr}"
                    )
                    self.assertEqual(result.returncode, 0, failure_msg)

    def test_checker_refuses_planted_commands(self) -> None:
        problems = check_settings(
            planted_settings("./.claude/hooks/guard-bash.sh"), REPO_ROOT
        )
        self.assertTrue(problems, "expected problem for bare relative command path")

        problems = check_settings(
            planted_settings("$CLAUDE_PROJECT_DIR/.claude/hooks/guard-bash.sh"),
            REPO_ROOT,
        )
        self.assertTrue(problems, "expected problem for unquoted $CLAUDE_PROJECT_DIR")

        problems = check_settings(
            planted_settings('"$CLAUDE_PROJECT_DIR"/.claude/hooks/no-such.sh'),
            REPO_ROOT,
        )
        self.assertTrue(
            any("not git-tracked" in p for p in problems),
            f"expected not git-tracked problem in: {problems}",
        )

    def test_checker_boundaries(self) -> None:
        self.assertEqual(check_settings({}, REPO_ROOT), ["no hook commands"])
        self.assertEqual(
            check_settings({"hooks": {"PreToolUse": []}}, REPO_ROOT),
            ["no hook commands"],
        )

        many = planted_settings('"$CLAUDE_PROJECT_DIR"/.claude/hooks/guard-bash.sh')
        group = many["hooks"]["PreToolUse"][0]
        group["hooks"] = group["hooks"] * (MAX_HOOK_ENTRIES + 1)
        problems = check_settings(many, REPO_ROOT)
        self.assertTrue(
            any("exceeds" in p for p in problems),
            f"expected limit problem in: {problems}",
        )

        with self.assertRaises(ValueError):
            json.loads(
                '{"hooks": {}, "hooks": {}}',
                object_pairs_hook=reject_duplicate_keys,
            )


if __name__ == "__main__":
    unittest.main()
