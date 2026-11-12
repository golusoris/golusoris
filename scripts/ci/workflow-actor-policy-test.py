#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Workflow and action files allow only listed pull-request actor checks.

Local action targets must be scanned files.
"""

from __future__ import annotations

import re
import sys
import tempfile
import unittest
from dataclasses import dataclass
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[2]
WORKFLOW_DIRS: tuple[str, ...] = (".github/workflows", ".gitea/workflows")
ACTION_DIRS: tuple[str, ...] = (".github", ".gitea")
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

# (workflow path, job id, stripped line) -> reason.
ALLOWED_LINES: dict[tuple[str, str, str], str] = {
    (
        ".github/workflows/regenerate.yml",
        "<top>",
        "- 'renovate/**'",
    ): "Regenerate workflow triggers only on Renovate branches.",
    (
        ".gitea/workflows/renovate-on-demand.yml",
        "<top>",
        "name: renovate-on-demand",
    ): "On-demand Renovate workflow display name.",
    (
        ".gitea/workflows/renovate-on-demand.yml",
        "<top>",
        "group: renovate-${{ github.repository }}",
    ): "On-demand Renovate concurrency group.",
    (
        ".gitea/workflows/renovate-on-demand.yml",
        "renovate",
        "renovate:",
    ): "On-demand Renovate job identifier.",
    (
        ".gitea/workflows/renovate-on-demand.yml",
        "renovate",
        "image: docker.io/renovate/renovate:44@"
        "sha256:0423c5dfb1488df5716b6584032b3bcf7091ad7a3bc547c5707f047da7a30674",
    ): "Pinned Renovate container image for on-demand Gitea runs.",
    (
        ".gitea/workflows/renovate-on-demand.yml",
        "renovate",
        "- name: Run Renovate scoped to this repository",
    ): "On-demand Renovate execution step name.",
    (
        ".gitea/workflows/renovate-on-demand.yml",
        "renovate",
        "RENOVATE_PLATFORM: gitea",
    ): "Renovate CLI target platform setting.",
    (
        ".gitea/workflows/renovate-on-demand.yml",
        "renovate",
        "RENOVATE_ENDPOINT: https://git.dev.cauda.dev/api/v1",
    ): "Renovate CLI Gitea API endpoint setting.",
    (
        ".gitea/workflows/renovate-on-demand.yml",
        "renovate",
        'RENOVATE_AUTODISCOVER: "false"',
    ): "Renovate CLI repository autodiscovery disabled.",
    (
        ".gitea/workflows/renovate-on-demand.yml",
        "renovate",
        "RENOVATE_REPOSITORIES: ${{ github.repository }}",
    ): "Renovate CLI single-repository scope.",
    (
        ".gitea/workflows/renovate-on-demand.yml",
        "renovate",
        "RENOVATE_GIT_AUTHOR: Renovate Bot <renovate@cauda.dev>",
    ): "Renovate CLI commit author identity.",
    (
        ".gitea/workflows/renovate-on-demand.yml",
        "renovate",
        'RENOVATE_FETCH_CHANGE_LOGS: "off"',
    ): "Renovate CLI changelog fetching disabled.",
    (
        ".gitea/workflows/renovate-on-demand.yml",
        "renovate",
        "RENOVATE_TOKEN: ${{ secrets.RENOVATE_TOKEN }}",
    ): "Renovate CLI Gitea authentication secret.",
    (
        ".gitea/workflows/renovate-on-demand.yml",
        "renovate",
        "GITHUB_COM_TOKEN: ${{ secrets.RENOVATE_GITHUB_COM_TOKEN }}",
    ): "Renovate CLI GitHub release-notes token secret.",
    (
        ".gitea/workflows/renovate-on-demand.yml",
        "renovate",
        "run: renovate",
    ): "On-demand Renovate CLI invocation.",
    (
        ".github/workflows/ci.yml",
        "ci-success",
        "ACTOR: ${{ github.actor }}",
    ): "check-required-jobs.sh bot skip input.",
    (
        ".github/workflows/rebuild-on-base.yml",
        "rebuild",
        "username: ${{ github.actor }}",
    ): "GHCR docker/login-action username.",
    (
        ".github/workflows/release-go.yml",
        "goreleaser",
        "username: ${{ github.actor }}",
    ): "GHCR docker/login-action username.",
    (
        ".github/workflows/tiny-litert-sidecar-image.yml",
        "publish",
        "username: ${{ github.actor }}",
    ): "GHCR docker/login-action username.",
    (
        ".github/workflows/tiny-trainer-images.yml",
        "publish",
        "username: ${{ github.actor }}",
    ): "GHCR docker/login-action username.",
    (
        ".github/workflows/verify-provenance.yml",
        "verify",
        "username: ${{ github.actor }}",
    ): "GHCR docker/login-action username.",
}

_INDEX_ACCESS = re.compile(
    r"(?:(?<=[A-Za-z0-9_\]\)])\s*)?\[\s*(?P<q>['\"])(?P<key>[A-Za-z_][A-Za-z0-9_-]*)(?P=q)\s*\]"
)
_ACTOR_TOKEN = re.compile(
    r"\b(?:"
    r"(?:github|gitea)\.(?:actor_id|triggering_actor|actor)"
    r"|(?:sender|user|actor)\.(?:login|id|node_id|type)"
    r"|author_association"
    r"|pusher\.(?:name|email)"
    r"|(?:author|committer)\.(?:name|email|username)"
    r"|GITHUB_(?:ACTOR_ID|TRIGGERING_ACTOR|ACTOR)"
    r"|GITEA_(?:TRIGGERING_ACTOR|ACTOR)"
    r")\b",
    re.IGNORECASE,
)
_BOT_LITERAL = re.compile(r"renovate|dependabot|\[bot\]", re.IGNORECASE)
_TOP_KEY = re.compile(
    r"^(?P<q>['\"]?)(?P<key>[A-Za-z0-9_-]+)(?P=q)[ \t]*:(?P<rest>.*)$"
)
_JOB_KEY = re.compile(
    r"^  (?P<q>['\"]?)(?P<job>[A-Za-z0-9_-]+)(?P=q)[ \t]*:(?P<rest>.*)$"
)
_JOB_IF_KEY = re.compile(r"^    if:(?:\s|$)")
_JOB_IF_LINE = re.compile(r"^    if:\s*(?P<expr>.+?)\s*$")
_BLOCK_SCALAR = re.compile(
    r"^(?:\s*-\s+|\s*[^:#]+:\s*)[|>](?:[1-9][+-]?|[+-][1-9]?)?(?:\s+#.*)?\s*$"
)
_SEQ_MAP_PREFIX = re.compile(r"^(?P<prefix>\s*-\s+)[^:#]+:")
_USES_KEY = re.compile(r"^(?:-[ \t]+)?(?P<kq>['\"]?)uses(?P=kq)[ \t]*:")
_USES_LINE = re.compile(
    r"^(?:-[ \t]+)?(?P<kq>['\"]?)uses(?P=kq)[ \t]*:[ \t]*(?P<val>.*?)[ \t]*$"
)
_QUOTED_SCALAR = re.compile(r"^(?P<q>['\"])(?P<inner>.*?)(?P=q)(?:[ \t]+#.*)?$")
_INLINE_COMMENT = re.compile(r"[ \t]+#.*$")


@dataclass
class _AllowanceHits:
    allowed_ifs: set[tuple[str, str, str]]
    allowed_lines: set[tuple[str, str, str]]


@dataclass(frozen=True)
class _LocalScanContext:
    root: Path
    scanned_paths: frozenset[Path]


@dataclass(frozen=True)
class _LoadedFile:
    name: str
    text: str
    error: str | None = None


def _block_scalar_indent(line: str, indent: int) -> int | None:
    """Return the parent indent when line opens a YAML block scalar."""
    if _BLOCK_SCALAR.match(line) is None:
        return None
    seq_match = _SEQ_MAP_PREFIX.match(line)
    if seq_match is not None:
        return len(seq_match.group("prefix"))
    return indent


def _normalise_index_access(line: str) -> str:
    """Normalise bracket property access x['name'] and x["name"] to x.name."""
    return _INDEX_ACCESS.sub(r".\g<key>", line)


def _has_disallowed_token(line: str) -> bool:
    """Return True when line contains a disallowed actor token or bot literal."""
    if _BOT_LITERAL.search(line) is not None:
        return True
    return (
        _ACTOR_TOKEN.search(line) is not None
        or _ACTOR_TOKEN.search(_normalise_index_access(line)) is not None
    )


@dataclass
class _ScanState:
    in_jobs: bool = False
    current_job: str = "<top>"
    job_if_count: int = 0
    block_scalar: int | None = None
    uses_block: bool = False

    def advance_section(self, line: str, indent: int) -> None:
        """Update top-level YAML section, current job, and job-if count."""
        if indent == 0:
            top_match = _TOP_KEY.match(line)
            if top_match is not None:
                self.in_jobs = top_match.group("key") == "jobs"
                self.current_job = "<top>"
                self.job_if_count = 0
            return
        if not self.in_jobs:
            return
        if indent == 2:
            job_match = _JOB_KEY.match(line)
            if job_match is not None:
                self.current_job = job_match.group("job")
                self.job_if_count = 0
        elif self.current_job != "<top>" and _JOB_IF_KEY.match(line) is not None:
            self.job_if_count += 1

    def advance_structure(self, line: str, indent: int) -> None:
        """Advance YAML structure and block-scalar state for allowance scoping."""
        self.advance_section(line, indent)
        block_indent = _block_scalar_indent(line, indent)
        if block_indent is not None:
            self.block_scalar = block_indent
            self.uses_block = _USES_KEY.match(line.lstrip(" \t")) is not None


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


def _is_allowed_job_if(
    name: str,
    line: str,
    state: _ScanState,
    hits: _AllowanceHits | None,
) -> bool:
    """Return True when line matches an allow-listed pull-request-only job if."""
    if not state.in_jobs or state.current_job == "<top>" or state.job_if_count != 1:
        return False
    if_match = _JOB_IF_LINE.fullmatch(line)
    if if_match is None:
        return False
    if_key = (name, state.current_job, _normalise_if_expr(if_match.group("expr")))
    if not ALLOWED.get(if_key, "").strip():
        return False
    if hits is not None:
        hits.allowed_ifs.add(if_key)
    return True


def _is_allowed_actor_line(
    name: str,
    line: str,
    state: _ScanState,
    hits: _AllowanceHits | None = None,
) -> bool:
    """Return True only for allow-listed exact lines or pull-request-only job ifs."""
    line_key = (name, state.current_job, line.strip())
    if ALLOWED_LINES.get(line_key, "").strip():
        if hits is not None:
            hits.allowed_lines.add(line_key)
        return True
    return _is_allowed_job_if(name, line, state, hits)


def _unquote_uses_value(raw: str) -> str | None:
    """Extract a local ./target value from raw YAML scalar text."""
    val = raw.strip()
    quoted = _QUOTED_SCALAR.match(val)
    target = (
        quoted.group("inner")
        if quoted is not None
        else _INLINE_COMMENT.sub("", val).strip()
    )
    return target if target.startswith("./") else None


def _extract_local_uses(stripped: str) -> str | None:
    """Return the ./target path when stripped is a local uses entry."""
    match = _USES_LINE.match(stripped)
    if match is None:
        return None
    return _unquote_uses_value(match.group("val"))


def _has_symlink_component(root: Path, target: str) -> bool:
    """Return True when any path segment traversed under root is a symlink."""
    current = root
    for part in Path(target).parts[:MAX_WORKFLOW_FILES]:
        current = current / part
        if current.is_symlink():
            return True
    return False


def _is_scanned_local_action(target: str, ctx: _LocalScanContext) -> bool:
    """Return True when local uses target resolves to a scanned file."""
    if _has_symlink_component(ctx.root, target):
        return False
    try:
        resolved = (ctx.root / target).resolve(strict=True)
    except (OSError, ValueError):
        return False
    if resolved.is_file():
        return resolved in ctx.scanned_paths
    for filename in ("action.yml", "action.yaml"):
        entry = resolved / filename
        if entry.exists() or entry.is_symlink():
            return not entry.is_symlink() and entry.resolve() in ctx.scanned_paths
    return False


def _check_local_uses(
    name: str,
    lineno: int,
    target: str | None,
    job: str,
    local_ctx: _LocalScanContext | None,
) -> str | None:
    """Return a finding when a local uses target is outside the scanned set."""
    if local_ctx is None or target is None:
        return None
    if _is_scanned_local_action(target, local_ctx):
        return None
    return f"{name}:{lineno}:{job}: local action not scanned: {target}"


def _consume_block_scalar(
    name: str,
    lineno: int,
    line: str,
    stripped: str,
    indent: int,
    state: _ScanState,
    local_ctx: _LocalScanContext | None = None,
) -> tuple[bool, list[str]]:
    """Handle lines while inside a YAML block scalar."""
    if state.block_scalar is None:
        return False, []
    if not stripped:
        return True, []
    if indent <= state.block_scalar:
        state.block_scalar = None
        state.uses_block = False
        return False, []
    findings: list[str] = []
    if _has_disallowed_token(line):
        findings.append(
            f"{name}:{lineno}:{state.current_job}: "
            f"disallowed actor reference: {stripped.strip()}"
        )
    if state.uses_block:
        uses_finding = _check_local_uses(
            name, lineno, _unquote_uses_value(stripped), state.current_job, local_ctx
        )
        if uses_finding is not None:
            findings.append(uses_finding)
    return True, findings


def _check_line(
    name: str,
    lineno: int,
    line: str,
    state: _ScanState,
    hits: _AllowanceHits | None = None,
    local_ctx: _LocalScanContext | None = None,
) -> list[str]:
    """Return policy findings for one workflow or action line."""
    stripped = line.lstrip(" \t")
    indent = len(line) - len(stripped)
    consumed, block_findings = _consume_block_scalar(
        name, lineno, line, stripped, indent, state, local_ctx
    )
    if consumed:
        return block_findings
    if not stripped or stripped.startswith("#"):
        return []
    state.advance_structure(line, indent)
    findings: list[str] = []
    if _has_disallowed_token(line) and not _is_allowed_actor_line(
        name, line, state, hits
    ):
        findings.append(
            f"{name}:{lineno}:{state.current_job}: "
            f"disallowed actor reference: {stripped.strip()}"
        )
    uses_finding = _check_local_uses(
        name, lineno, _extract_local_uses(stripped), state.current_job, local_ctx
    )
    if uses_finding is not None:
        findings.append(uses_finding)
    return findings


def _check_text(
    name: str,
    text: str,
    hits: _AllowanceHits | None = None,
    local_ctx: _LocalScanContext | None = None,
) -> list[str]:
    """Return actor-policy findings for one workflow file and record allowance hits."""
    lines = text.splitlines()
    if len(lines) > MAX_WORKFLOW_LINES:
        return [f"{name}:1:<top>: workflow exceeds {MAX_WORKFLOW_LINES} lines"]
    state = _ScanState()
    findings: list[str] = []
    for lineno, line in enumerate(lines[:MAX_WORKFLOW_LINES], start=1):
        findings.extend(_check_line(name, lineno, line, state, hits, local_ctx))
    return findings


def check(name: str, text: str) -> list[str]:
    """Return the actor-policy findings for one workflow file."""
    return _check_text(name, text)


def _collect_workflow_paths(root: Path) -> list[Path]:
    """Collect sorted, deduplicated workflow and action YAML paths."""
    candidates: list[Path] = []
    for directory in WORKFLOW_DIRS:
        workflow_dir = root / directory
        candidates.extend(
            sorted(
                [*workflow_dir.glob("*.yml"), *workflow_dir.glob("*.yaml")],
                key=lambda path: path.relative_to(root).as_posix(),
            )
        )
    for directory in ACTION_DIRS:
        action_dir = root / directory
        candidates.extend(
            sorted(
                [*action_dir.rglob("action.yml"), *action_dir.rglob("action.yaml")],
                key=lambda path: path.relative_to(root).as_posix(),
            )
        )
    return list(dict.fromkeys(candidates))


def _load_scanned_files(
    root: Path, paths: list[Path]
) -> tuple[list[_LoadedFile], frozenset[Path]]:
    """Read collected YAML files, recording load errors and resolved scanned paths."""
    loaded: list[_LoadedFile] = []
    scanned: set[Path] = set()
    for path in paths[:MAX_WORKFLOW_FILES]:
        name = path.relative_to(root).as_posix()
        if path.is_symlink():
            loaded.append(
                _LoadedFile(name, "", f"{name}:1:<top>: workflow symlink refused")
            )
            continue
        try:
            text = path.read_text(encoding="utf-8")
        except (OSError, UnicodeDecodeError) as exc:
            loaded.append(
                _LoadedFile(
                    name,
                    "",
                    f"{name}:1:<top>: unreadable workflow: {type(exc).__name__}",
                )
            )
            continue
        loaded.append(_LoadedFile(name, text))
        scanned.add(path.resolve())
    return loaded, frozenset(scanned)


def scan_with_hits(
    root: Path,
) -> tuple[list[str], set[tuple[str, str, str]], set[tuple[str, str, str]]]:
    """Scan workflow and action files and return findings plus hit sets."""
    paths = _collect_workflow_paths(root)
    hits = _AllowanceHits(allowed_ifs=set(), allowed_lines=set())
    if len(paths) > MAX_WORKFLOW_FILES:
        return (
            [f"{root}:1:<top>: workflow count exceeds {MAX_WORKFLOW_FILES}"],
            hits.allowed_ifs,
            hits.allowed_lines,
        )
    loaded, scanned_paths = _load_scanned_files(root, paths)
    local_ctx = _LocalScanContext(root=root.resolve(), scanned_paths=scanned_paths)
    findings: list[str] = []
    for item in loaded[:MAX_WORKFLOW_FILES]:
        if item.error is not None:
            findings.append(item.error)
            continue
        findings.extend(_check_text(item.name, item.text, hits, local_ctx))
    return findings, hits.allowed_ifs, hits.allowed_lines


def scan(root: Path) -> list[str]:
    """Scan workflow and composite action YAML files under root."""
    findings, _, _ = scan_with_hits(root)
    return findings


def _write_yaml(root: Path, rel: str, content: str) -> None:
    """Write UTF-8 YAML fixture text at rel under root."""
    target = root / rel
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(content, encoding="utf-8")


class WorkflowActorPolicyTest(unittest.TestCase):
    def test_allowed_entries_are_pull_request_only(self) -> None:
        self.assertEqual(len(ALLOWED), 3)
        for (path, job, expr), reason in ALLOWED.items():
            self.assertTrue(path and job and reason.strip())
            self.assertTrue(
                expr.endswith("&& github.event_name == 'pull_request'"),
                f"{path}:{job}: {expr}",
            )

    def test_all_allowances_are_hit_by_repository_scan(self) -> None:
        self.assertEqual(len(ALLOWED_LINES), 21)
        for (path, job, line_text), reason in list(ALLOWED_LINES.items())[
            :MAX_WORKFLOW_LINES
        ]:
            self.assertTrue(path and job and line_text and reason.strip())
        findings, hit_ifs, hit_lines = scan_with_hits(REPO_ROOT)
        self.assertEqual(findings, [])
        self.assertEqual(hit_ifs, set(ALLOWED))
        self.assertEqual(hit_lines, set(ALLOWED_LINES))

    def test_scan_with_hits_omits_unhit_allowances(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            workflows = root / ".github/workflows"
            workflows.mkdir(parents=True)
            (workflows / "ok.yml").write_text(
                "on:\n  push:\njobs:\n  ok:\n    runs-on: ubuntu-26.04\n",
                encoding="utf-8",
            )
            findings, hit_ifs, hit_lines = scan_with_hits(root)
            self.assertEqual(findings, [])
            self.assertEqual(hit_ifs, set())
            self.assertEqual(hit_lines, set())

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

    def test_block_scalar_if_actor_id_fails(self) -> None:
        text = (
            "on:\n  pull_request:\n"
            "jobs:\n  semgrep:\n    if: >-\n"
            "      github.actor_id != 29139614\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertTrue(
            findings[0].startswith(".github/workflows/security-scan.yml:6:semgrep:"),
            findings[0],
        )

    def test_run_and_other_actor_tokens_fail(self) -> None:
        text = (
            "on:\n  push:\n"
            "jobs:\n  build:\n    steps:\n"
            "      - run: |\n"
            "          username: ${{ github.actor }}\n"
            "      - if: github.triggering_actor != vars.BOT_LOGIN\n"
            "        run: echo ${{ github.event.sender.login }} ${{ github.event.issue.user.login }}\n"
        )
        findings = check(".github/workflows/build.yml", text)
        self.assertEqual(len(findings), 3)
        self.assertIn(".github/workflows/build.yml:7:build:", findings[0])
        self.assertIn(".github/workflows/build.yml:8:build:", findings[1])
        self.assertIn(".github/workflows/build.yml:9:build:", findings[2])

    def test_username_actor_value_line_pass_and_fail_pair(self) -> None:
        text = (
            "on:\n  push:\n"
            "jobs:\n  goreleaser:\n    steps:\n"
            "      - uses: docker/login-action@v4\n"
            "        with:\n"
            "          username: ${{ github.actor }}\n"
        )
        self.assertEqual(check(".github/workflows/release-go.yml", text), [])
        findings = check(".github/workflows/other.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertTrue(
            findings[0].startswith(".github/workflows/other.yml:8:goreleaser:"),
            findings[0],
        )
        wrong_job_findings = check(
            ".github/workflows/release-go.yml",
            text.replace("  goreleaser:\n", "  publish:\n", 1),
        )
        self.assertEqual(len(wrong_job_findings), 1)
        self.assertTrue(
            wrong_job_findings[0].startswith(
                ".github/workflows/release-go.yml:8:publish:"
            ),
            wrong_job_findings[0],
        )
        run_block_findings = check(
            ".github/workflows/release-go.yml",
            "on:\n  push:\njobs:\n  goreleaser:\n    steps:\n"
            "      - run: |\n          username: ${{ github.actor }}\n",
        )
        self.assertEqual(len(run_block_findings), 1)
        self.assertTrue(
            run_block_findings[0].startswith(
                ".github/workflows/release-go.yml:7:goreleaser:"
            ),
            run_block_findings[0],
        )

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

    def test_github_actor_id_job_if_fails(self) -> None:
        text = (
            "on:\n  pull_request:\n"
            "jobs:\n  semgrep:\n"
            "    if: github.actor_id != 29139614\n"
            "    runs-on: ubuntu-26.04\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertIn(".github/workflows/security-scan.yml:5:semgrep:", findings[0])

    def test_gitea_workflow_actor_tokens_fail(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            workflows = root / ".gitea/workflows"
            workflows.mkdir(parents=True)
            (workflows / "ci.yml").write_text(
                "on:\n  push:\n"
                "jobs:\n  lint:\n"
                "    if: gitea.actor != vars.BOT_LOGIN\n"
                "    steps:\n"
                "      - if: gitea.triggering_actor != vars.BOT_LOGIN\n"
                "        run: echo ${{ gitea.actor_id }}\n",
                encoding="utf-8",
            )
            findings = scan(root)
            self.assertEqual(len(findings), 3)
            self.assertIn(".gitea/workflows/ci.yml:5:lint:", findings[0])
            self.assertIn(".gitea/workflows/ci.yml:7:lint:", findings[1])
            self.assertIn(".gitea/workflows/ci.yml:8:lint:", findings[2])

    def test_step_user_id_and_sender_id_if_fail(self) -> None:
        text = (
            "on:\n  pull_request:\n"
            "jobs:\n  verify:\n    steps:\n"
            "      - if: github.event.pull_request.user.id != 29139614\n"
            "        run: echo pr\n"
            "      - if: github.event.sender.id != 29139614\n"
            "        run: echo sender\n"
        )
        findings = check(".github/workflows/ci.yml", text)
        self.assertEqual(len(findings), 2)
        self.assertIn(".github/workflows/ci.yml:6:verify:", findings[0])
        self.assertIn(".github/workflows/ci.yml:8:verify:", findings[1])

    def test_non_github_actor_value_lines_fail(self) -> None:
        text = (
            "on:\n  push:\n"
            "jobs:\n  publish:\n    steps:\n"
            "      - env:\n"
            "          ACTOR_ID: ${{ github.actor_id }}\n"
            "          GITEA_ACTOR: ${{ gitea.actor }}\n"
            "        run: echo ok\n"
        )
        findings = check(".github/workflows/release-go.yml", text)
        self.assertEqual(len(findings), 2)
        self.assertIn(".github/workflows/release-go.yml:7:publish:", findings[0])
        self.assertIn(".github/workflows/release-go.yml:8:publish:", findings[1])

    def test_actor_token_word_boundaries_pass(self) -> None:
        text = (
            "on:\n  push:\n"
            "jobs:\n  build:\n    steps:\n"
            "      - env:\n"
            "          A: ${{ inputs.my_github.actor }}\n"
            "          B: ${{ inputs.github.actor_identity }}\n"
            "          C: ${{ inputs.x_gitea.actor_id }}\n"
            "          D: ${{ inputs.gitea.actors }}\n"
            "          E: ${{ inputs.app_user.id }}\n"
            "          F: ${{ inputs.custom_sender.id }}\n"
            "          G: ${{ inputs.user.identifier }}\n"
            "          H: ${{ inputs.sender.identity }}\n"
            "          I: githubxactor github_actors gitea_actor_id sender_id user_id\n"
            "          J: ${{ inputs.my_pusher.name }} ${{ inputs.pusher.names }} ${{ inputs.pusher.emails }}\n"
            "          K: ${{ inputs.co_author.name }} ${{ inputs.author.names }} ${{ inputs.author.emails }} ${{ inputs.author.usernames }}\n"
            "          L: ${{ inputs.my_committer.email }} ${{ inputs.committer.names }} ${{ inputs.committer.emails }} ${{ inputs.committer.usernames }}\n"
            "          M: ${{ inputs.app_user.type }} ${{ inputs.custom_sender.type }} ${{ inputs.user.types }} ${{ inputs.sender.types }}\n"
            "          N: ${{ inputs.my_actor.id }} ${{ inputs.actor.identifier }} ${{ inputs.user.node_identifier }}\n"
            "          O: ${{ inputs.my_author_association }} MY_GITHUB_ACTOR GITHUB_ACTOR_NAME MY_GITEA_ACTOR GITEA_ACTOR_NAME\n"
            "        run: echo ok\n"
        )
        self.assertEqual(check(".github/workflows/build.yml", text), [])

    def test_job_if_head_ref_renovate_fails(self) -> None:
        text = (
            "on:\n  push:\n"
            "jobs:\n  semgrep:\n"
            "    if: ${{ !startsWith(github.head_ref, 'renovate/') }}\n"
            "    runs-on: ubuntu-26.04\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertTrue(
            findings[0].startswith(".github/workflows/security-scan.yml:5:semgrep:"),
            findings[0],
        )

    def test_step_dash_if_user_type_fails(self) -> None:
        text = (
            "on:\n  pull_request:\n"
            "jobs:\n  verify:\n    steps:\n"
            "      - if: github.event.pull_request.user.type != 'Bot'\n"
            "        run: echo check\n"
        )
        findings = check(".github/workflows/ci.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertTrue(
            findings[0].startswith(".github/workflows/ci.yml:6:verify:"),
            findings[0],
        )

    def test_job_if_pusher_name_fails(self) -> None:
        text = (
            "on:\n  push:\n"
            "jobs:\n  trivy:\n"
            "    if: github.event.pusher.name != 'renovate[bot]'\n"
            "    runs-on: ubuntu-26.04\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertTrue(
            findings[0].startswith(".github/workflows/security-scan.yml:5:trivy:"),
            findings[0],
        )

    def test_step_key_if_author_username_fails(self) -> None:
        text = (
            "on:\n  push:\n"
            "jobs:\n  scan:\n    steps:\n"
            "      - name: Run scanner\n"
            "        if: github.event.head_commit.author.username != 'renovate-bot'\n"
            "        run: echo scan\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertTrue(
            findings[0].startswith(".github/workflows/security-scan.yml:7:scan:"),
            findings[0],
        )

    def test_job_block_scalar_if_dependabot_fails(self) -> None:
        text = (
            "on:\n  pull_request:\n"
            "jobs:\n  semgrep:\n    if: >-\n"
            "      !startsWith(github.head_ref, 'dependabot/')\n"
            "    runs-on: ubuntu-26.04\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertTrue(
            findings[0].startswith(".github/workflows/security-scan.yml:6:semgrep:"),
            findings[0],
        )

    def test_renovate_on_demand_lines_pass_and_fail_pair(self) -> None:
        text = (REPO_ROOT / ".gitea/workflows/renovate-on-demand.yml").read_text(
            encoding="utf-8"
        )
        self.assertEqual(check(".gitea/workflows/renovate-on-demand.yml", text), [])
        findings = check(".github/workflows/other.yml", text)
        expected = (
            (5, "<top>"),
            (13, "<top>"),
            (17, "renovate"),
            (24, "renovate"),
            (27, "renovate"),
            (29, "renovate"),
            (30, "renovate"),
            (31, "renovate"),
            (32, "renovate"),
            (33, "renovate"),
            (34, "renovate"),
            (37, "renovate"),
            (38, "renovate"),
            (39, "renovate"),
        )
        self.assertEqual(len(findings), len(expected))
        for idx, (lineno, job) in enumerate(expected):
            self.assertTrue(
                findings[idx].startswith(
                    f".github/workflows/other.yml:{lineno}:{job}:"
                ),
                findings[idx],
            )

    def test_quoted_job_if_keys_fail(self) -> None:
        text = (
            "on:\n  push:\n"
            "jobs:\n  semgrep:\n"
            "    \"if\": ${{ !startsWith(github.head_ref, 'renovate/') }}\n"
            "  trivy:\n"
            "    'if': ${{ !startsWith(github.head_ref, 'renovate/') }}\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 2)
        self.assertTrue(
            findings[0].startswith(".github/workflows/security-scan.yml:5:semgrep:"),
            findings[0],
        )
        self.assertTrue(
            findings[1].startswith(".github/workflows/security-scan.yml:7:trivy:"),
            findings[1],
        )

    def test_explicit_indent_block_scalar_if_fails(self) -> None:
        text = (
            "on:\n  push:\n"
            "jobs:\n  semgrep:\n"
            "    if: >2-\n"
            "      !startsWith(github.head_ref, 'renovate/')\n"
            "  trivy:\n"
            "    if: |2-\n"
            "      !startsWith(github.head_ref, 'renovate/')\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 2)
        self.assertTrue(
            findings[0].startswith(".github/workflows/security-scan.yml:6:semgrep:"),
            findings[0],
        )
        self.assertTrue(
            findings[1].startswith(".github/workflows/security-scan.yml:9:trivy:"),
            findings[1],
        )

    def test_jobs_key_with_space_before_colon_fails(self) -> None:
        text = (
            "on:\n  push:\n"
            "jobs :\n  lint:\n"
            "    if: ${{ !startsWith(github.head_ref, 'renovate/') }}\n"
            "  trivy :\n"
            "    if: github.actor_id != 29139614\n"
            '  "audit"\t:\n'
            "    if: github.actor_id != 29139614\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        expected = ((5, "lint"), (7, "trivy"), (9, "audit"))
        self.assertEqual(len(findings), len(expected))
        for idx, (lineno, job) in enumerate(expected):
            self.assertTrue(
                findings[idx].startswith(
                    f".github/workflows/security-scan.yml:{lineno}:{job}:"
                ),
                findings[idx],
            )
        tab_top = check(
            ".github/workflows/security-scan.yml",
            'on:\n  push:\n"jobs"\t:\n  lint:\n    if: github.actor_id != 29139614\n',
        )
        self.assertEqual(len(tab_top), 1)
        self.assertTrue(
            tab_top[0].startswith(".github/workflows/security-scan.yml:5:lint:"),
            tab_top[0],
        )

    def test_index_syntax_actor_tokens_fail(self) -> None:
        text = (
            "on:\n  pull_request:\n"
            "jobs:\n  semgrep:\n"
            "    if: ${{ github['actor_id'] != '29139614' }}\n"
            "  trivy:\n"
            '    if: github["actor"] != vars.BOT_LOGIN\n'
            "  chained:\n"
            "    if: github['event']['workflow_run']['actor']['login'] != vars.BOT_LOGIN\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 3)
        self.assertTrue(
            findings[0].startswith(".github/workflows/security-scan.yml:5:semgrep:"),
            findings[0],
        )
        self.assertIn("github['actor_id']", findings[0])
        self.assertTrue(
            findings[1].startswith(".github/workflows/security-scan.yml:7:trivy:"),
            findings[1],
        )
        self.assertIn('github["actor"]', findings[1])
        self.assertTrue(
            findings[2].startswith(".github/workflows/security-scan.yml:9:chained:"),
            findings[2],
        )

    def test_workflow_run_actor_id_fails(self) -> None:
        text = (
            "on:\n  workflow_run:\n"
            "jobs:\n  semgrep:\n"
            "    if: github.event.workflow_run.actor.id != 29139614\n"
            "    steps:\n"
            "      - if: github.event.workflow_run.actor.node_id != vars.BOT_NODE_ID\n"
            "        run: echo one\n"
            "      - if: github.event.workflow_run.actor.type != 'Bot'\n"
            "        run: echo two\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        expected = (5, 7, 9)
        self.assertEqual(len(findings), len(expected))
        for idx, lineno in enumerate(expected):
            self.assertTrue(
                findings[idx].startswith(
                    f".github/workflows/security-scan.yml:{lineno}:semgrep:"
                ),
                findings[idx],
            )

    def test_author_association_fails(self) -> None:
        text = (
            "on:\n  pull_request:\n"
            "jobs:\n  semgrep:\n"
            "    if: github.event.pull_request.author_association != 'BOT'\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertTrue(
            findings[0].startswith(".github/workflows/security-scan.yml:5:semgrep:"),
            findings[0],
        )

    def test_user_node_id_fails(self) -> None:
        text = (
            "on:\n  pull_request:\n"
            "jobs:\n  semgrep:\n"
            "    if: github.event.pull_request.user.node_id != vars.BOT_NODE_ID\n"
            "    steps:\n"
            "      - if: github.event.sender.node_id != vars.BOT_NODE_ID\n"
            "        run: echo ok\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 2)
        self.assertTrue(
            findings[0].startswith(".github/workflows/security-scan.yml:5:semgrep:"),
            findings[0],
        )
        self.assertTrue(
            findings[1].startswith(".github/workflows/security-scan.yml:7:semgrep:"),
            findings[1],
        )

    def test_step_actor_env_and_default_actor_env_vars_fail(self) -> None:
        text = (
            "on:\n  push:\n"
            "jobs:\n  semgrep:\n    steps:\n"
            "      - env:\n"
            "          ACTOR: ${{ github.actor }}\n"
            "        run: echo ok\n"
            '      - run: test "$GITHUB_ACTOR" != "$SKIP" || exit 0\n'
            '      - run: echo "$GITHUB_ACTOR_ID"\n'
            '      - run: echo "$GITHUB_TRIGGERING_ACTOR"\n'
            '      - run: echo "$GITEA_ACTOR"\n'
            '      - run: echo "$GITEA_TRIGGERING_ACTOR"\n'
        )
        findings = check(".github/workflows/security-scan.yml", text)
        expected = (7, 9, 10, 11, 12, 13)
        self.assertEqual(len(findings), len(expected))
        for idx, lineno in enumerate(expected):
            self.assertTrue(
                findings[idx].startswith(
                    f".github/workflows/security-scan.yml:{lineno}:semgrep:"
                ),
                findings[idx],
            )

    def test_step_run_bot_literal_compare_fails(self) -> None:
        text = (
            "on:\n  push:\n"
            "jobs:\n  semgrep:\n    steps:\n"
            "      - run: '[ \"$ACTOR\" = renovate ] && exit 0'\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertTrue(
            findings[0].startswith(".github/workflows/security-scan.yml:6:semgrep:"),
            findings[0],
        )

    def test_scan_invalid_utf8_workflow_reports_unreadable_finding(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            workflows = root / ".github/workflows"
            workflows.mkdir(parents=True)
            (workflows / "broken.yml").write_bytes(b"name: \xff\xfe\n")
            findings = scan(root)
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
            findings = scan(root)
            self.assertEqual(len(findings), 1)
            self.assertTrue(
                findings[0].startswith(
                    ".github/workflows/dir.yaml:1:<top>: unreadable workflow: "
                ),
                findings[0],
            )

    def test_identity_field_tokens_without_bot_literal_fail(self) -> None:
        text = (
            "jobs:\n  audit:\n"
            "    if: github.event.sender.type != 'Bot'\n"
            "    steps:\n"
            "      - if: github.event.pusher.name != 'ci'\n"
            "        env:\n"
            "          PE: ${{ github.event.pusher.email }}\n"
            "          AN: ${{ github.event.head_commit.author.name }}\n"
            "          AE: ${{ github.event.head_commit.author.email }}\n"
            "          AU: ${{ github.event.head_commit.author.username }}\n"
            "          CN: ${{ github.event.head_commit.committer.name }}\n"
            "          CE: ${{ github.event.head_commit.committer.email }}\n"
            "        run: echo ${{ github.event.head_commit.committer.username }}\n"
        )
        findings = check(".github/workflows/audit.yml", text)
        expected = (3, 5, 7, 8, 9, 10, 11, 12, 13)
        self.assertEqual(len(findings), len(expected))
        for idx, lineno in enumerate(expected):
            self.assertTrue(
                findings[idx].startswith(
                    f".github/workflows/audit.yml:{lineno}:audit:"
                ),
                findings[idx],
            )

    def test_bot_literal_case_and_pipe_block_if_fail(self) -> None:
        text = (
            "jobs:\n  gate:\n"
            "    if: !contains(github.event.pull_request.title, 'Dependabot')\n"
            "    steps:\n"
            "      - if: !startsWith(github.head_ref, 'RENOVATE/')\n"
            "        run: echo one\n"
            "      - name: Check suffix\n"
            "        if: !endsWith(inputs.label, '[BOT]')\n"
            "        run: echo two\n"
            "      - name: Pipe block\n"
            "        if: |\n"
            "          !contains(github.head_ref, '[bot]')\n"
            "        run: echo three\n"
        )
        findings = check(".github/workflows/gate.yml", text)
        self.assertEqual(len(findings), 4)
        self.assertTrue(findings[0].startswith(".github/workflows/gate.yml:3:gate:"))
        self.assertTrue(findings[1].startswith(".github/workflows/gate.yml:5:gate:"))
        self.assertTrue(findings[2].startswith(".github/workflows/gate.yml:8:gate:"))
        self.assertTrue(findings[3].startswith(".github/workflows/gate.yml:12:gate:"))

    def test_actor_token_case_insensitive_fails(self) -> None:
        text = (
            "on:\n  pull_request:\n"
            "jobs:\n  semgrep:\n"
            "    if: github.Actor != vars.BOT_LOGIN\n"
            "  trivy:\n"
            "    if: ${{ GITHUB['ACTOR_ID'] != '29139614' }}\n"
            "    steps:\n"
            "      - if: Gitea.Triggering_Actor != vars.BOT_LOGIN\n"
            "        run: echo ${{ github.event.Sender.Login }} "
            "${{ github.event.pull_request.Author_Association }}\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        expected = ((5, "semgrep"), (7, "trivy"), (9, "trivy"), (10, "trivy"))
        self.assertEqual(len(findings), len(expected))
        for idx, (lineno, job) in enumerate(expected):
            self.assertTrue(
                findings[idx].startswith(
                    f".github/workflows/security-scan.yml:{lineno}:{job}:"
                ),
                findings[idx],
            )

    def test_indent_then_chomping_block_scalar_if_actor_id_fails(self) -> None:
        text = (
            "on:\n  pull_request:\n"
            "jobs:\n  semgrep:\n"
            "    if: >2-\n"
            "      github.actor_id != 29139614\n"
            "  trivy:\n"
            "    if: |2+\n"
            "      github.actor_id != 29139614\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 2)
        self.assertTrue(
            findings[0].startswith(".github/workflows/security-scan.yml:6:semgrep:"),
            findings[0],
        )
        self.assertTrue(
            findings[1].startswith(".github/workflows/security-scan.yml:9:trivy:"),
            findings[1],
        )
        run_blocks = check(
            ".github/workflows/release-go.yml",
            "on:\n  push:\njobs:\n  goreleaser:\n    steps:\n"
            "      - run: >2-\n          username: ${{ github.actor }}\n"
            "      - run: |2+\n          username: ${{ github.actor }}\n"
            "      - run: >-2\n          username: ${{ github.actor }}\n"
            "      - run: |+2\n          username: ${{ github.actor }}\n",
        )
        self.assertEqual(len(run_blocks), 4)
        for idx, lineno in enumerate((7, 9, 11, 13)):
            self.assertTrue(
                run_blocks[idx].startswith(
                    f".github/workflows/release-go.yml:{lineno}:goreleaser:"
                ),
                run_blocks[idx],
            )

    def test_run_indent_chomping_block_scalar_keeps_tracked_job(self) -> None:
        indent_one = check(
            ".github/workflows/ci.yml",
            "on:\n  push:\njobs:\n  semgrep:\n    steps:\n"
            "      - run: echo ok\n"
            " run: >2-\n  pr-title:\n end: 1\n"
            "    if: github.actor_id != 29139614\n"
            "  lint:\n"
            "    if: github.actor_id != 29139614\n",
        )
        self.assertEqual(len(indent_one), 2)
        self.assertTrue(
            indent_one[0].startswith(".github/workflows/ci.yml:10:semgrep:"),
            indent_one[0],
        )
        self.assertTrue(
            indent_one[1].startswith(".github/workflows/ci.yml:12:lint:"),
            indent_one[1],
        )
        step_run = check(
            ".github/workflows/ci.yml",
            "on:\n  push:\njobs:\n  semgrep:\n    steps:\n"
            "      - run: >2-\n          pr-title:\n"
            "      - if: github.actor_id != 29139614\n        run: echo ok\n"
            "  lint:\n"
            "    if: github.actor_id != 29139614\n",
        )
        self.assertEqual(len(step_run), 2)
        self.assertTrue(
            step_run[0].startswith(".github/workflows/ci.yml:8:semgrep:"),
            step_run[0],
        )
        self.assertTrue(
            step_run[1].startswith(".github/workflows/ci.yml:11:lint:"),
            step_run[1],
        )

    def test_composite_action_actor_if_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            action = root / ".github/actions/x/action.yml"
            nested = root / ".github/actions/x/nested/action.yml"
            nested.parent.mkdir(parents=True)
            (root / ".github/actions/action.yml").write_text(
                "runs:\n  using: composite\n  steps:\n"
                "    - if: github.actor != vars.BOT_LOGIN\n",
                encoding="utf-8",
            )
            nested.write_text(
                "runs:\n  using: composite\n  steps:\n"
                "    - if: github.actor != vars.BOT_LOGIN\n",
                encoding="utf-8",
            )
            action.write_text(
                "name: Gate\nruns:\n  using: composite\n  steps:\n"
                "    - if: github.actor != vars.BOT_LOGIN\n"
                "      shell: bash\n      run: echo check\n",
                encoding="utf-8",
            )
            findings = scan(root)
            expected = (
                ".github/actions/action.yml:4:<top>:",
                ".github/actions/x/action.yml:5:<top>:",
                ".github/actions/x/nested/action.yml:4:<top>:",
            )
            self.assertEqual(len(findings), len(expected))
            for idx, prefix in enumerate(expected):
                self.assertTrue(findings[idx].startswith(prefix), findings[idx])

    def test_local_action_outside_scanned_set_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            _write_yaml(
                root,
                "tools/gate/action.yml",
                "runs:\n  using: composite\n  steps:\n    - run: echo ok\n",
            )
            _write_yaml(
                root,
                ".github/workflows/ci.yml",
                "on:\n  push:\njobs:\n  gate-job:\n    steps:\n"
                "      - uses: ./tools/gate\n",
            )
            self.assertEqual(
                scan(root),
                [
                    ".github/workflows/ci.yml:6:gate-job: "
                    "local action not scanned: ./tools/gate"
                ],
            )

    def test_missing_local_action_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            _write_yaml(
                root,
                ".github/workflows/ci.yml",
                "on:\n  push:\njobs:\n  build:\n    steps:\n"
                "      - uses: ./.github/actions/missing\n",
            )
            self.assertEqual(
                scan(root),
                [
                    ".github/workflows/ci.yml:6:build: "
                    "local action not scanned: ./.github/actions/missing"
                ],
            )

    def test_scanned_local_action_yaml_and_reusable_workflow_pass(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            _write_yaml(
                root,
                ".github/actions/x/action.yaml",
                "runs:\n  using: composite\n  steps:\n"
                "    - shell: bash\n      run: echo ok\n",
            )
            _write_yaml(
                root,
                ".github/workflows/reuse.yml",
                "on:\n  workflow_call:\njobs:\n  inner:\n"
                "    runs-on: ubuntu-26.04\n    steps:\n      - run: echo ok\n",
            )
            _write_yaml(
                root,
                ".github/workflows/ci.yml",
                "on:\n  push:\njobs:\n  build:\n    steps:\n"
                "      - uses: ./.github/actions/x\n"
                "  call-reuse:\n"
                "    uses: ./.github/workflows/reuse.yml\n",
            )
            self.assertEqual(scan(root), [])

    def test_quoted_local_action_outside_scanned_set_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            _write_yaml(
                root,
                "tools/gate/action.yml",
                "runs:\n  using: composite\n  steps:\n    - run: echo ok\n",
            )
            _write_yaml(
                root,
                ".github/workflows/ci.yml",
                "on:\n  push:\njobs:\n  gate-job:\n    steps:\n"
                "      - uses: './tools/gate'\n",
            )
            self.assertEqual(
                scan(root),
                [
                    ".github/workflows/ci.yml:6:gate-job: "
                    "local action not scanned: ./tools/gate"
                ],
            )

    def test_local_action_boundaries_and_gitea_depth_fail_closed(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            _write_yaml(
                root,
                ".github/actions/clean/action.yml",
                "runs:\n  using: composite\n  steps:\n    - run: echo ok\n",
            )
            (root / ".github/actions/empty").mkdir(parents=True)
            _write_yaml(root, "tools/reuse.yml", "on:\n  workflow_call:\n")
            (root / "tools/link").symlink_to(root / ".github/actions/clean")
            _write_yaml(
                root,
                ".github/workflows/action.yml",
                "jobs:\n  w:\n    if: github.actor_id != 1\n",
            )
            _write_yaml(
                root,
                ".gitea/actions/sub/deep/action.yaml",
                "runs:\n  using: composite\n  steps:\n"
                "    - if: gitea.actor != vars.BOT_LOGIN\n",
            )
            _write_yaml(
                root,
                ".github/workflows/z.yml",
                "jobs:\n  b:\n    steps:\n"
                "      # - uses: ./tools/ignored\n"
                '      - name: File target\n        uses: "./tools/reuse.yml" # note\n'
                "      - uses: ./.github/actions/empty\n"
                "      - uses: ./tools/link\n"
                "      - uses: >-\n          ./tools/gate\n",
            )
            self.assertEqual(
                scan(root),
                [
                    ".github/workflows/action.yml:3:w: "
                    "disallowed actor reference: if: github.actor_id != 1",
                    ".github/workflows/z.yml:6:b: "
                    "local action not scanned: ./tools/reuse.yml",
                    ".github/workflows/z.yml:7:b: "
                    "local action not scanned: ./.github/actions/empty",
                    ".github/workflows/z.yml:8:b: "
                    "local action not scanned: ./tools/link",
                    ".github/workflows/z.yml:10:b: "
                    "local action not scanned: ./tools/gate",
                    ".gitea/actions/sub/deep/action.yaml:4:<top>: "
                    "disallowed actor reference: - if: gitea.actor != vars.BOT_LOGIN",
                ],
            )

    def test_composite_action_renovate_literal_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            yaml_action = root / ".github/actions/x/action.yaml"
            yaml_action.parent.mkdir(parents=True)
            yaml_action.write_text(
                "runs:\n  using: composite\n  steps:\n"
                "    - shell: bash\n"
                "      run: '[ \"$ACTOR\" = renovate ] && exit 0'\n",
                encoding="utf-8",
            )
            findings = scan(root)
            self.assertEqual(len(findings), 1)
            self.assertTrue(
                findings[0].startswith(".github/actions/x/action.yaml:5:<top>:"),
                findings[0],
            )
            yml_action = root / ".github/actions/y/action.yml"
            yml_action.parent.mkdir(parents=True)
            yml_action.write_text(
                "runs:\n  using: composite\n  steps:\n"
                "    - if: !startsWith(github.head_ref, 'renovate/')\n",
                encoding="utf-8",
            )
            ordered = scan(root)
            self.assertEqual(len(ordered), 2)
            self.assertTrue(
                ordered[0].startswith(".github/actions/x/action.yaml:5:<top>:"),
                ordered[0],
            )
            self.assertTrue(
                ordered[1].startswith(".github/actions/y/action.yml:4:<top>:"),
                ordered[1],
            )

    def test_lowercase_actor_env_step_run_fails(self) -> None:
        text = (
            "on:\n  push:\n"
            "jobs:\n  semgrep:\n    steps:\n"
            "      - run: echo $env:github_actor\n"
        )
        findings = check(".github/workflows/security-scan.yml", text)
        self.assertEqual(len(findings), 1)
        self.assertTrue(
            findings[0].startswith(".github/workflows/security-scan.yml:6:semgrep:"),
            findings[0],
        )
        other_envs = check(
            ".github/workflows/security-scan.yml",
            "on:\n  push:\njobs:\n  semgrep:\n    steps:\n"
            "      - run: echo $env:github_actor_id\n"
            "      - run: echo $env:github_triggering_actor\n"
            "      - run: echo $env:gitea_actor\n"
            "      - run: echo $env:gitea_triggering_actor\n",
        )
        self.assertEqual(len(other_envs), 4)

    def test_gap_repo_variable_bot_prefix_if(self) -> None:
        """Known limit: repository variable exclusion carries no actor or bot token."""
        text = (
            "on:\n  pull_request:\n"
            "jobs:\n  semgrep:\n"
            "    if: ${{ !startsWith(github.head_ref, vars.BOT_PREFIX) }}\n"
            "    runs-on: ubuntu-26.04\n"
        )
        self.assertEqual(check(".github/workflows/security-scan.yml", text), [])

    def test_gap_script_file_step_run(self) -> None:
        """Known limit: actor logic inside an external script file is not read."""
        text = (
            "on:\n  push:\n"
            "jobs:\n  semgrep:\n    steps:\n"
            "      - run: bash scripts/ci/skip-bots.sh\n"
        )
        self.assertEqual(check(".github/workflows/security-scan.yml", text), [])

    def test_gap_third_party_action_uses_step(self) -> None:
        """Known limit: third-party action internals in uses steps are not scanned."""
        text = (
            "on:\n  push:\n"
            "jobs:\n  semgrep:\n    steps:\n"
            "      - uses: some-org/skip-bots@v1\n"
        )
        self.assertEqual(check(".github/workflows/security-scan.yml", text), [])

    def test_repository_workflows_comply(self) -> None:
        self.assertEqual(scan(REPO_ROOT), [])


if __name__ == "__main__":
    sys.exit(0 if unittest.main(exit=False).result.wasSuccessful() else 1)
