#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Workflow and action files allow only listed pull-request actor checks.

Identity matching is by object segment, every uses key and line separator must
use recognised YAML 1.2 forms, every uses value must match the local, remote,
or image shape, and local action targets must be scanned files.
"""

from __future__ import annotations

import re
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
        (
            "github.actor != 'renovate[bot]' "
            "&& github.actor != 'github-actions[bot]' "
            "&& github.event_name == 'pull_request'"
        ),
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
        (
            "image: docker.io/renovate/renovate:44@"
            "sha256:0423c5dfb1488df5716b6584032b3bcf7091ad7a3bc547c5707f047da7a30674"
        ),
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
# Match identity object segments directly so unlisted nested fields fail closed.
_IDENTITY_OBJECT = (
    r"(?:actor|actor_id|triggering_actor|sender|user|author|committer|pusher"
    r"|merged_by|assignee|requested_reviewer)"
)
_ACTOR_TOKEN = re.compile(
    r"(?:"
    rf"\.{_IDENTITY_OBJECT}\b"
    rf"|\b{_IDENTITY_OBJECT}\s*[.\[]"
    r"|\.login\b"
    r"|\b(?:github|gitea)\.(?:actor_id|triggering_actor|actor)\b"
    r"|\bauthor_association\b"
    r"|\bGIT(?:HUB|EA)_(?:ACTOR_ID|TRIGGERING_ACTOR|ACTOR)\b"
    r")",
    re.IGNORECASE,
)
_BOT_LITERAL = re.compile(r"renovate|dependabot|\[bot\]", re.IGNORECASE)
_COMPUTED_CONTEXT_INDEX = re.compile(
    r"\b(?:github|gitea)\b(?:\s*\.\s*(?:[A-Za-z_][A-Za-z0-9_-]*|\*))*\s*\[",
    re.IGNORECASE,
)
_STRING_BUILDING_CALL = re.compile(
    r"\b(?:format|join|fromJSON|toJSON)\s*\(", re.IGNORECASE
)
_IDENTITY_CONTEXT = re.compile(r"\b(?:github|gitea)\b", re.IGNORECASE)
_EXPRESSION_SPAN = re.compile(r"\$\{\{(.*?)\}\}", re.DOTALL)
_DOT_WHITESPACE = re.compile(r"\s*\.\s*")
_IF_LINE = re.compile(
    r"^[ \t]*(?:-[ \t]+)?(?P<key>(?P<q>['\"]?)if(?P=q))"
    r"[ \t]*:[ \t]*(?P<expr>.*)$",
    re.DOTALL,
)
_YAML_SIMPLE_ESCAPES = {
    "0": "\0",
    "a": "\a",
    "b": "\b",
    "t": "\t",
    "\t": "\t",
    "n": "\n",
    "v": "\v",
    "f": "\f",
    "r": "\r",
    "e": "\x1b",
    " ": " ",
    '"': '"',
    "/": "/",
    "\\": "\\",
    "N": "\x85",
    "_": "\xa0",
    "L": "\u2028",
    "P": "\u2029",
}
_YAML_ESCAPE = re.compile(
    r"\\(?:["
    + re.escape("".join(_YAML_SIMPLE_ESCAPES))
    + r"]|x[0-9A-Fa-f]{2}|u[0-9A-Fa-f]{4}|U[0-9A-Fa-f]{8})"
)
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
_USES_KEY_TOKEN = re.compile(r"\buses['\"]?[ \t]*:", re.IGNORECASE)
_EXPLICIT_KEY = re.compile(r"^(?:-[ \t]+)*\?(?:[ \t]|$)")
_FLOW_USES = re.compile(r"[{,][ \t]*['\"]?uses['\"]?[ \t]*:")
_UNEXPECTED_LINE_SEP = re.compile(r"[\x0b\x0c\x1c\x1d\x1e\x85\u2028\u2029]")
_QUOTED_SCALAR = re.compile(
    r"^(?:(?:'(?P<sq>[^']*)')|(?:\"(?P<dq>[^\"\\]*)\"))(?:[ \t]+#.*)?$"
)
_INLINE_COMMENT = re.compile(r"[ \t]+#.*$")
_LOCAL_USES = re.compile(r"\./[A-Za-z0-9_./-]+")
_REMOTE_USES = re.compile(
    r"(?!\.\.?/)[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_./-]+)?@[A-Za-z0-9_./-]+"
)
_IMAGE_USES = re.compile(r"docker://\S+")


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


def _decode_yaml_escape(match: re.Match[str]) -> str:
    """Decode a YAML escape, preserving invalid Unicode scalar values."""
    escape = match.group()[1:]
    if escape in _YAML_SIMPLE_ESCAPES:
        return _YAML_SIMPLE_ESCAPES[escape]
    codepoint = int(escape[1:], 16)
    if codepoint > 0x10FFFF or 0xD800 <= codepoint <= 0xDFFF:
        return match.group()
    return chr(codepoint)


def _expression_texts(line: str, if_block: bool) -> list[str]:
    """Collect inline expression spans and implicit if expressions."""
    if if_block:
        return [line]
    match = _IF_LINE.match(line)
    if match is not None:
        return [match.group("expr")]
    return [match.group(1) for match in _EXPRESSION_SPAN.finditer(line)]


def _expression_has_disallowed_token(expression: str, is_if: bool) -> bool:
    """Check lexer-equivalent member access and computed string expressions."""
    collapsed = _DOT_WHITESPACE.sub(".", _normalise_index_access(expression))
    if (
        _ACTOR_TOKEN.search(collapsed) is not None
        or _COMPUTED_CONTEXT_INDEX.search(collapsed) is not None
    ):
        return True
    return _STRING_BUILDING_CALL.search(expression) is not None and (
        is_if or _IDENTITY_CONTEXT.search(expression) is not None
    )


def _line_has_disallowed_token(line: str, if_block: bool) -> bool:
    """Check direct tokens and expression-only normalised forms."""
    normalised = _normalise_index_access(line)
    if (
        _BOT_LITERAL.search(line) is not None
        or _ACTOR_TOKEN.search(line) is not None
        or _ACTOR_TOKEN.search(normalised) is not None
        or _COMPUTED_CONTEXT_INDEX.search(normalised) is not None
    ):
        return True
    is_if = if_block or _IF_LINE.match(line) is not None
    return any(
        _expression_has_disallowed_token(expression, is_if)
        for expression in _expression_texts(line, if_block)
    )


def _has_disallowed_token(line: str, if_block: bool = False) -> bool:
    """Check both original text and its YAML escape-decoded copy."""
    return _line_has_disallowed_token(line, if_block) or _line_has_disallowed_token(
        _YAML_ESCAPE.sub(_decode_yaml_escape, line), if_block
    )


@dataclass
class _ScanState:
    in_jobs: bool = False
    current_job: str = "<top>"
    job_if_count: int = 0
    block_scalar: int | None = None
    uses_block: bool = False
    if_block: bool = False
    previous_if_line: str = ""
    pending_if_indent: int | None = None

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
        if_match = _IF_LINE.match(line)
        self.pending_if_indent = if_match.start("key") if if_match is not None else None
        block_indent = _block_scalar_indent(line, indent)
        if block_indent is not None:
            self.block_scalar = block_indent
            self.uses_block = _USES_KEY.match(line.lstrip(" \t")) is not None
            self.if_block = if_match is not None
            self.previous_if_line = ""
            self.pending_if_indent = None

    def consume_if_continuation(self, stripped: str, indent: int) -> bool:
        """Refuse a deeper plain continuation after a non-block if value."""
        previous_indent = self.pending_if_indent
        self.pending_if_indent = None
        return (
            previous_indent is not None
            and indent > previous_indent
            and _TOP_KEY.match(stripped) is None
            and re.match(r"^-[ \t]", stripped) is None
        )


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


def _unquote_uses_value(raw: str) -> str:
    """Strip one pair of matching quotes and any trailing YAML comment."""
    val = raw.strip()
    quoted = _QUOTED_SCALAR.match(val)
    if quoted is not None:
        return quoted.group("sq") or quoted.group("dq") or ""
    return _INLINE_COMMENT.sub("", val).strip()


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
    target: str,
    job: str,
    local_ctx: _LocalScanContext | None,
) -> str | None:
    """Return a finding when a local uses target is outside the scanned set."""
    if local_ctx is None or _is_scanned_local_action(target, local_ctx):
        return None
    return f"{name}:{lineno}:{job}: local action not scanned: {target}"


def _check_uses_value(
    name: str,
    lineno: int,
    raw: str,
    job: str,
    local_ctx: _LocalScanContext | None,
) -> str | None:
    """Validate one uses scalar against local, remote, and image allow-lists."""
    val = raw.strip()
    target = _unquote_uses_value(val)
    if _LOCAL_USES.fullmatch(target) is not None:
        return _check_local_uses(name, lineno, target, job, local_ctx)
    if (
        _REMOTE_USES.fullmatch(target) is not None
        or _IMAGE_USES.fullmatch(target) is not None
    ):
        return None
    return f"{name}:{lineno}:{job}: uses value not recognised: {val}"


def _check_uses_line(
    name: str,
    lineno: int,
    line: str,
    stripped: str,
    state: _ScanState,
    local_ctx: _LocalScanContext | None,
) -> list[str]:
    """Return flow-style, key-form, and allow-list findings on a non-block line."""
    findings: list[str] = []
    flow_refused = _FLOW_USES.search(line) is not None
    if flow_refused:
        findings.append(f"{name}:{lineno}:{state.current_job}: flow-style uses refused")
    match = _USES_LINE.match(stripped)
    key_count = len(_USES_KEY_TOKEN.findall(line))
    if match is None or key_count != 1:
        if key_count and not flow_refused:
            findings.extend(
                [f"{name}:{lineno}:{state.current_job}: uses key form not recognised"]
                * key_count
            )
        return findings
    if state.uses_block:
        return findings
    uses_finding = _check_uses_value(
        name, lineno, match.group("val"), state.current_job, local_ctx
    )
    if uses_finding is not None:
        findings.append(uses_finding)
    return findings


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
        state.if_block = False
        state.previous_if_line = ""
        return False, []
    findings: list[str] = []
    token_line = line
    if state.if_block:
        token_line = state.previous_if_line + " " + line
        state.previous_if_line = line
    if _has_disallowed_token(token_line, state.if_block):
        findings.append(
            f"{name}:{lineno}:{state.current_job}: "
            f"disallowed actor reference: {stripped.strip()}"
        )
    if state.uses_block:
        uses_finding = _check_uses_value(
            name, lineno, stripped, state.current_job, local_ctx
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
    multiline_if = state.consume_if_continuation(stripped, indent)
    state.advance_structure(line, indent)
    findings: list[str] = []
    if multiline_if:
        findings.append(f"{name}:{lineno}:{state.current_job}: multi-line if refused")
    if line.rstrip().endswith("\\"):
        findings.append(
            f"{name}:{lineno}:{state.current_job}: escaped line break refused"
        )
    if _has_disallowed_token(line) and not _is_allowed_actor_line(
        name, line, state, hits
    ):
        findings.append(
            f"{name}:{lineno}:{state.current_job}: "
            f"disallowed actor reference: {stripped.strip()}"
        )
    if _EXPLICIT_KEY.match(stripped) is not None:
        findings.append(f"{name}:{lineno}:{state.current_job}: explicit key refused")
    findings.extend(_check_uses_line(name, lineno, line, stripped, state, local_ctx))
    return findings


def _check_text(
    name: str,
    text: str,
    hits: _AllowanceHits | None = None,
    local_ctx: _LocalScanContext | None = None,
) -> list[str]:
    """Return actor-policy findings for one workflow file and record allowance hits."""
    if _UNEXPECTED_LINE_SEP.search(text) is not None:
        return [f"{name}:1:<top>: unexpected line separator"]
    lines = text.splitlines()
    if len(lines) > MAX_WORKFLOW_LINES:
        return [f"{name}:1:<top>: workflow exceeds {MAX_WORKFLOW_LINES} lines"]
    state = _ScanState()
    findings: list[str] = []
    for lineno, line in enumerate(lines[:MAX_WORKFLOW_LINES], start=1):
        if line.count("${{") > line.count("}}"):
            findings.append(
                f"{name}:{lineno}:{state.current_job}: expression spans lines refused"
            )
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


def write_fixture(root: Path, rel: str, content: str) -> None:
    """Write UTF-8 YAML fixture text at rel under root."""
    target = root / rel
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(content, encoding="utf-8")
