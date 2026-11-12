#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Every workflow actor condition is pull-request-only and explicitly allow-listed."""

from __future__ import annotations

import re
import sys
import tempfile
import unittest
from dataclasses import dataclass
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
WORKFLOW_DIRS: tuple[str, ...] = (".github/workflows", ".gitea/workflows")
MAX_WORKFLOW_FILES = 256
MAX_WORKFLOW_LINES = 8192

# (workflow path, job id, normalised expression) -> reason.
ALLOWED: dict[tuple[str, str, str], str] = {
    (
        ".github/workflows/ci.yml",
        "pr-title",
        "github.actor != 'renovate[bot]' && github.event_name == 'pull_request'",
    ): "Renovate PR titles follow its commit convention; check-required-jobs.sh skips it.",
    (
        ".github/workflows/ci.yml",
        "dco",
        "github.actor != 'renovate[bot]' "
        "&& github.actor != 'github-actions[bot]' "
        "&& github.event_name == 'pull_request'",
    ): "Bot PRs carry no human DCO sign-off; check-required-jobs.sh skips them.",
    (
        ".github/workflows/ci-go.yml",
        "pr-title",
        "github.actor != 'renovate[bot]' && github.event_name == 'pull_request'",
    ): "Reusable Go CI PR-title check mirrors ci.yml on caller pull requests.",
}

_ACTOR_TOKEN = re.compile(
    r"\b(?:github\.actor|github\.triggering_actor|sender\.login|user\.login)\b"
)
_TOP_KEY = re.compile(r"^(?P<q>['\"]?)(?P<key>[A-Za-z0-9_-]+)(?P=q):(?P<rest>.*)$")
_JOB_KEY = re.compile(r"^  (?P<q>['\"]?)(?P<job>[A-Za-z0-9_-]+)(?P=q):(?P<rest>.*)$")
_JOB_IF_KEY = re.compile(r"^    if:(?:\s|$)")
_JOB_IF_LINE = re.compile(r"^    if:\s*(?P<expr>.+?)\s*$")
_ACTOR_VALUE_LINE = re.compile(
    r"^\s*(?P<key>[A-Za-z_][A-Za-z0-9_-]*): \$\{\{ github\.actor \}\}$"
)
_BLOCK_SCALAR = re.compile(r"^(?:\s*-\s+|\s*[^:#]+:\s*)[|>][+-]?[1-9]?(?:\s+#.*)?\s*$")


@dataclass
class _ScanState:
    in_jobs: bool = False
    current_job: str = "<top>"
    job_if_count: int = 0
    block_indent: int | None = None

    def advance_structure(self, line: str, indent: int) -> None:
        """Update YAML section, current job, and block-scalar state."""
        top_match = _TOP_KEY.match(line) if indent == 0 else None
        if top_match is not None:
            self.in_jobs = top_match.group("key") == "jobs"
            self.current_job = "<top>"
            self.job_if_count = 0
        elif self.in_jobs and indent == 2:
            job_match = _JOB_KEY.match(line)
            if job_match is not None:
                self.current_job = job_match.group("job")
                self.job_if_count = 0
        elif self.in_jobs and self.current_job != "<top>" and _JOB_IF_KEY.match(line):
            self.job_if_count += 1
        if _BLOCK_SCALAR.match(line) is not None:
            self.block_indent = indent


def _normalise_if_expr(raw: str) -> str:
    """Strip an outer ${{ }} wrapper and collapse internal whitespace."""
    expr = raw.strip()
    if (
        expr.startswith("${{")
        and expr.endswith("}}")
        and "${{" not in expr[3:-2]
        and "}}" not in expr[3:-2]
    ):
        expr = expr[3:-2]
    return " ".join(expr.split())


def _is_allowed_actor_line(
    name: str,
    job: str,
    line: str,
    *,
    in_jobs: bool,
    job_if_count: int,
) -> bool:
    """Return True only for exact github.actor values or allow-listed job ifs."""
    value_match = _ACTOR_VALUE_LINE.fullmatch(line)
    if value_match is not None:
        return value_match.group("key") not in {"if", "run"}
    if not in_jobs or job == "<top>" or job_if_count != 1:
        return False
    if_match = _JOB_IF_LINE.fullmatch(line)
    if if_match is None:
        return False
    expr = _normalise_if_expr(if_match.group("expr"))
    return bool(ALLOWED.get((name, job, expr), "").strip())


def _consume_block_scalar(
    name: str,
    lineno: int,
    line: str,
    stripped: str,
    indent: int,
    state: _ScanState,
) -> tuple[bool, str | None]:
    """Handle lines while inside a YAML block scalar."""
    if state.block_indent is None:
        return False, None
    if not stripped:
        return True, None
    if indent > state.block_indent:
        if _ACTOR_TOKEN.search(line) is None:
            return True, None
        return (
            True,
            f"{name}:{lineno}:{state.current_job}: "
            f"disallowed actor reference: {stripped.strip()}",
        )
    state.block_indent = None
    return False, None


def _check_line(
    name: str,
    lineno: int,
    line: str,
    state: _ScanState,
) -> str | None:
    """Return a finding if a workflow line carries an unlisted actor reference."""
    stripped = line.lstrip(" \t")
    indent = len(line) - len(stripped)
    consumed, block_finding = _consume_block_scalar(
        name, lineno, line, stripped, indent, state
    )
    if consumed:
        return block_finding
    if not stripped or stripped.startswith("#"):
        return None
    state.advance_structure(line, indent)
    if _ACTOR_TOKEN.search(line) is None:
        return None
    if _is_allowed_actor_line(
        name,
        state.current_job,
        line,
        in_jobs=state.in_jobs,
        job_if_count=state.job_if_count,
    ):
        return None
    return (
        f"{name}:{lineno}:{state.current_job}: "
        f"disallowed actor reference: {stripped.strip()}"
    )


def check(name: str, text: str) -> list[str]:
    """Return the actor-policy findings for one workflow file."""
    lines = text.splitlines()
    if len(lines) > MAX_WORKFLOW_LINES:
        return [f"{name}:1:<top>: workflow exceeds {MAX_WORKFLOW_LINES} lines"]
    state = _ScanState()
    findings: list[str] = []
    for lineno, line in enumerate(lines[:MAX_WORKFLOW_LINES], start=1):
        finding = _check_line(name, lineno, line, state)
        if finding is not None:
            findings.append(finding)
    return findings


def scan(root: Path) -> list[str]:
    """Scan all workflow YAML files under .github/workflows and .gitea/workflows."""
    paths: list[Path] = []
    for directory in WORKFLOW_DIRS:
        workflow_dir = root / directory
        paths.extend(
            sorted([*workflow_dir.glob("*.yml"), *workflow_dir.glob("*.yaml")])
        )
    if len(paths) > MAX_WORKFLOW_FILES:
        return [f"{root}:1:<top>: workflow count exceeds {MAX_WORKFLOW_FILES}"]
    findings: list[str] = []
    for path in paths[:MAX_WORKFLOW_FILES]:
        name = path.relative_to(root).as_posix()
        if path.is_symlink():
            findings.append(f"{name}:1:<top>: workflow symlink refused")
            continue
        findings.extend(check(name, path.read_text(encoding="utf-8")))
    return findings


class WorkflowActorPolicyTest(unittest.TestCase):
    def test_allowed_entries_are_pull_request_only(self) -> None:
        self.assertEqual(len(ALLOWED), 3)
        for (path, job, expr), reason in ALLOWED.items():
            self.assertTrue(path and job and reason.strip())
            self.assertTrue(
                expr.endswith("&& github.event_name == 'pull_request'"),
                f"{path}:{job}: {expr}",
            )

    def test_push_workflow_actor_if_fails(self) -> None:
        text = (
            "on:\n  push:\n    branches: [main]\n"
            "jobs:\n  semgrep:\n    if: github.actor != 'renovate[bot]'\n"
            "    runs-on: ubuntu-26.04\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertIn(".github/workflows/security-scan.yml:6:semgrep:", findings[0])

    def test_wrapped_expression_if_fails(self) -> None:
        text = (
            "on:\n  workflow_call:\n"
            "jobs:\n  spectral:\n"
            "    if: ${{ github.actor != 'renovate[bot]' && inputs.openapi-spec != '' }}\n"
        )
        findings = check(".github/workflows/ci-go.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertIn(".github/workflows/ci-go.yml:5:spectral:", findings[0])

    def test_unlisted_pr_scoped_expression_fails(self) -> None:
        text = (
            "on:\n  pull_request:\n"
            "jobs:\n  semgrep:\n"
            "    if: github.actor != 'renovate[bot]' && github.event_name == 'pull_request'\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertIn(".github/workflows/security-scan.yml:5:semgrep:", findings[0])

    def test_block_scalar_if_naming_actor_fails(self) -> None:
        text = (
            "on:\n  pull_request:\n"
            "jobs:\n  pr-title:\n    if: >-\n"
            "      github.actor != 'renovate[bot]' && github.event_name == 'pull_request'\n"
        )
        findings = check(".github/workflows/ci.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertIn(".github/workflows/ci.yml:6:pr-title:", findings[0])

    def test_run_and_other_actor_tokens_fail(self) -> None:
        text = (
            "on:\n  push:\n"
            "jobs:\n  build:\n    steps:\n"
            "      - run: |\n"
            "          username: ${{ github.actor }}\n"
            "      - if: github.triggering_actor != 'renovate[bot]'\n"
            "        run: echo ${{ github.event.sender.login }} ${{ github.event.issue.user.login }}\n"
        )
        findings = check(".github/workflows/build.yml", text)
        self.assertEqual(len(findings), 3)
        self.assertIn(".github/workflows/build.yml:7:build:", findings[0])
        self.assertIn(".github/workflows/build.yml:8:build:", findings[1])
        self.assertIn(".github/workflows/build.yml:9:build:", findings[2])

    def test_username_actor_value_line_passes(self) -> None:
        text = (
            "on:\n  push:\n"
            "jobs:\n  publish:\n    steps:\n"
            "      - uses: docker/login-action@v4\n"
            "        with:\n"
            "          username: ${{ github.actor }}\n"
            "      - env:\n"
            "          ACTOR: ${{ github.actor }}\n"
            "        run: echo ok\n"
        )
        self.assertEqual(check(".github/workflows/release-go.yml", text), [])

    def test_allowed_entry_passes_plain_and_wrapped(self) -> None:
        text = (
            "on:\n  pull_request:\n"
            "jobs:\n  pr-title:\n"
            "    if: github.actor != 'renovate[bot]' && github.event_name == 'pull_request'\n"
            "  dco:\n"
            "    if: ${{  github.actor != 'renovate[bot]'   && github.actor != 'github-actions[bot]' && github.event_name == 'pull_request'  }}\n"
        )
        self.assertEqual(check(".github/workflows/ci.yml", text), [])

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
            findings = scan(root)
            self.assertEqual(len(findings), 2)
            self.assertIn(".github/workflows/a.yaml:3:a:", findings[0])
            self.assertIn(".gitea/workflows/b.yml:3:b:", findings[1])

    def test_repository_workflows_comply(self) -> None:
        self.assertEqual(scan(REPO_ROOT), [])


if __name__ == "__main__":
    sys.exit(0 if unittest.main(exit=False).result.wasSuccessful() else 1)
