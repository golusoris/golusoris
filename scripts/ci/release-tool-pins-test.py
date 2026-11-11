#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Regression tests proving release workflow tool pins agree with assertions."""

from __future__ import annotations

import json
import re
import tempfile
import unittest
from pathlib import Path
from typing import NamedTuple

from composite_expand import (
    COMPOSITE_ACTIONS,
    MAX_LINES,
    expand_workflow,
    read_bounded_lines,
    scalar_values,
    substitute_inputs,
    write_expanded_workflows,
)

REPO_ROOT = Path(__file__).resolve().parents[2]

WORKFLOW_FILES: tuple[str, ...] = (
    ".github/workflows/release-build.yml",
    ".github/workflows/release-go.yml",
    ".github/workflows/release-tool-contract.yml",
    ".github/workflows/rebuild-on-base.yml",
    ".github/workflows/tiny-litert-sidecar-image.yml",
    ".github/workflows/tiny-trainer-images.yml",
    ".github/workflows/ci.yml",
    ".github/workflows/verify-provenance.yml",
)

GROUP_A_FILES: tuple[str, ...] = (
    ".github/workflows/release-build.yml",
    ".github/workflows/release-tool-contract.yml",
    ".github/workflows/tiny-litert-sidecar-image.yml",
    ".github/workflows/tiny-trainer-images.yml",
    ".github/workflows/ci.yml",
)

GROUP_B_FILES: tuple[str, ...] = (
    ".github/workflows/release-go.yml",
    ".github/workflows/rebuild-on-base.yml",
    ".github/workflows/verify-provenance.yml",
)

REQUIRED_TOOLS_BY_FILE: dict[str, tuple[str, ...]] = {
    ".github/workflows/release-go.yml": ("node",),
    ".github/workflows/release-tool-contract.yml": ("node",),
}

TOOL_ACTIONS: dict[str, tuple[str, str]] = {
    "sigstore/cosign-installer": ("cosign", "cosign-release"),
    "anchore/sbom-action/download-syft": ("syft", "syft-version"),
    "goreleaser/goreleaser-action": ("goreleaser", "version"),
    "docker/setup-buildx-action": ("buildx", "version"),
    "actions/setup-node": ("node", "node-version"),
}

TOOL_ENV_VARS: dict[str, str] = {
    "cosign": "COSIGN_VERSION",
    "syft": "SYFT_VERSION",
    "goreleaser": "GORELEASER_VERSION",
    "buildx": "BUILDX_VERSION",
    "node": "NODE_VERSION",
}

ENV_TOOL_VARS: dict[str, str] = {
    "cosign": "COSIGN_VERSION",
    "syft": "SYFT_VERSION",
    "goreleaser": "GORELEASER_VERSION",
    "buildx": "BUILDX_VERSION",
}

EXPECTED_INPUTS: dict[str, str] = {
    "cosign": "v${{ env.COSIGN_VERSION }}",
    "syft": "v${{ env.SYFT_VERSION }}",
    "goreleaser": "v${{ env.GORELEASER_VERSION }}",
    "buildx": "v${{ env.BUILDX_VERSION }}",
    "node": "${{ env.NODE_VERSION }}",
}

ASSERTION_PATTERNS: dict[str, re.Pattern[str]] = {
    "cosign": re.compile(
        r'cosign_version="\$\(cosign\s+version\)";\s*grep\s+-Eq\s+.*<<<"\$cosign_version"'
    ),
    "syft": re.compile(
        r'syft_version="\$\(syft\s+version\)";\s*grep\s+-Eq\s+.*<<<"\$syft_version"'
    ),
    "goreleaser": re.compile(
        r'goreleaser_version="\$\(goreleaser\s+--version\)";\s*grep\s+-Eq\s+.*<<<"\$goreleaser_version"'
    ),
    "buildx": re.compile(
        r'buildx_version="\$\(docker\s+buildx\s+version\)";\s*grep\s+-Eq\s+.*<<<"\$buildx_version"'
    ),
    "node": re.compile(r"test\s+.*node\s+--version|node\s+--version\s*\|"),
}

NODE_ASSERTION_EXACT = 'test "$(node --version)" = "v${NODE_VERSION}"'
NODE_RENOVATE_COMMENT = "# renovate: datasource=node-version depName=node versioning=node"

ALL_RELEASE_VARS = frozenset(TOOL_ENV_VARS.values())

ACTION_LINE_RE = re.compile(r"^\s*(?:-\s+)?uses:\s+([\w/-]+)@[0-9a-f]{40}\b")
LITERAL_VERSION_RE = re.compile(r"\bv?\d+(?:\\*\.\d+){2,}\b")
ENV_LINE_RE = re.compile(r"^([A-Z0-9_]+)=(.*)$")
RUN_INLINE_RE = re.compile(r"^(?P<pre>\s*(?:-\s+)?)run:\s+(?P<cmd>[^|>\s].*)$")
RUN_BLOCK_RE = re.compile(r"^(?P<pre>\s*(?:-\s+)?)run:\s*[|>][-+0-9]*\s*(?:#.*)?$")
MAX_FILES = 16
LOOKAHEAD_LINES = 6


class Pin(NamedTuple):
    file: str
    line: int
    tool: str
    version: str


class Assertion(NamedTuple):
    file: str
    line: int
    tool: str
    version: str


def is_comment_line(line: str) -> bool:
    # Identifies comment lines starting with hash after leading whitespace.
    return line.lstrip().startswith("#")


def compile_js_regex(pattern: str) -> re.Pattern[str]:
    """Converts JS named capture syntax (?<name>...) to Python syntax (?P<name>...)."""
    return re.compile(re.sub(r"\(\?<([A-Za-z][A-Za-z0-9_]*)>", r"(?P<\1>", pattern))


def matches_file(pattern: str, path: str) -> bool:
    """Evaluates Renovate slash-regex against file path."""
    if len(pattern) < 2 or pattern[0] != "/" or pattern[-1] != "/":
        return False
    return compile_js_regex(pattern[1:-1]).search(path) is not None


def parse_action_line(
    idx: int,
    line: str,
    lines: list[str],
    total: int,
    rel_path: str,
    pins: list[Pin],
    problems: list[str],
) -> None:
    """Extracts action version pin within bounded lookahead window and verifies expression."""
    if is_comment_line(line):
        return
    match = ACTION_LINE_RE.search(line)
    if not match:
        return
    action = match.group(1)
    if action not in TOOL_ACTIONS:
        return
    tool, key = TOOL_ACTIONS[action]
    expected_expr = EXPECTED_INPUTS[tool]
    var_name = TOOL_ENV_VARS[tool]
    prefix = f"{key}:"
    end = min(total, idx + 1 + LOOKAHEAD_LINES)
    for look_idx in range(idx + 1, end):
        if is_comment_line(lines[look_idx]):
            continue
        stripped = lines[look_idx].strip()
        if stripped.startswith(prefix):
            val = stripped[len(prefix):].strip().strip("'\"")
            if val != expected_expr:
                problems.append(
                    f"{rel_path}:{look_idx + 1}: {tool}: literal pin {val}; read ${{{{ env.{var_name} }}}}"
                )
            pins.append(Pin(rel_path, look_idx + 1, tool, val))
            return
    problems.append(f"{rel_path}:{idx + 1}: {action} has no {key} pin")


def parse_assertion_line(
    idx: int,
    line: str,
    rel_path: str,
    assertions: list[Assertion],
    problems: list[str],
) -> None:
    """Validates assertion line against literal version and variable expression requirements."""
    if is_comment_line(line):
        return
    line = line.rstrip()
    for tool, pattern in ASSERTION_PATTERNS.items():
        if not pattern.search(line):
            continue
        if LITERAL_VERSION_RE.search(line):
            problems.append(
                f"{rel_path}:{idx + 1}: {tool}: assertion contains literal version"
            )
        var_name = TOOL_ENV_VARS[tool]
        if tool == "node":
            if NODE_ASSERTION_EXACT not in line or "|| true" in line:
                problems.append(
                    f"{rel_path}:{idx + 1}: node: assertion must equal {NODE_ASSERTION_EXACT}"
                )
            assertions.append(Assertion(rel_path, idx + 1, tool, var_name))
        else:
            expected_ref = f"${{{var_name}//./\\\\.}}"
            if expected_ref not in line:
                problems.append(
                    f"{rel_path}:{idx + 1}: {tool}: assertion does not reference {expected_ref}"
                )
            assertions.append(Assertion(rel_path, idx + 1, tool, var_name))
        return


def run_block_end(lines: list[str], start: int, key_col: int) -> int:
    # First non-blank line indented at or left of the run key column ends the block.
    for j in range(start, len(lines)):
        line = lines[j]
        if line.strip() and len(line) - len(line.lstrip()) <= key_col:
            return j
    return len(lines)


def collect_run_lines(lines: list[str]) -> list[tuple[int, str]]:
    # Only text inside a step's run value may satisfy a version assertion.
    found: list[tuple[int, str]] = []
    skip_until = 0
    for idx, line in enumerate(lines):
        if idx < skip_until or is_comment_line(line):
            continue
        block = RUN_BLOCK_RE.match(line)
        if block:
            end = run_block_end(lines, idx + 1, len(block.group("pre")))
            found.extend((j, lines[j]) for j in range(idx + 1, end))
            skip_until = end
            continue
        inline = RUN_INLINE_RE.match(line)
        if inline:
            found.append((idx, inline.group("cmd")))
    return found


def parse_workflow_file(
    path: Path, rel_path: str
) -> tuple[list[Pin], list[Assertion], list[str], list[str]]:
    """Parses action pins and assertions in single workflow pass."""
    if not path.is_file():
        return [], [], [], [f"{rel_path}:1: tool: file does not exist"]
    lines = expand_workflow(path, path.parents[2]).splitlines(keepends=True)
    pins: list[Pin] = []
    assertions: list[Assertion] = []
    problems: list[str] = []
    total = len(lines)
    for idx, line in enumerate(lines):
        parse_action_line(idx, line, lines, total, rel_path, pins, problems)
    for idx, text in collect_run_lines(lines):
        parse_assertion_line(idx, text, rel_path, assertions, problems)
    return pins, assertions, lines, problems


def check_scope_coverage(
    workflow_files: tuple[str, ...],
    pins_by_file: dict[str, list[Pin]],
) -> list[str]:
    """Rule d: every file in scope yields at least one install pin."""
    return [
        f"{rel_path}:1: tool: file yielded no install pins"
        for rel_path in workflow_files[:MAX_FILES]
        if not pins_by_file.get(rel_path)
    ]


def check_workflow_node_and_required_tools(
    rel_path: str,
    pins: list[Pin],
    assertions: list[Assertion],
) -> list[str]:
    """Enforces required tool pins/assertions and node pin-assertion pairing."""
    problems: list[str] = []
    pin_tools = {p.tool for p in pins}
    assertion_tools = {a.tool for a in assertions}
    for tool in REQUIRED_TOOLS_BY_FILE.get(rel_path, ())[:MAX_FILES]:
        if tool not in pin_tools:
            problems.append(f"{rel_path}: {tool}: missing install pin")
        if tool not in assertion_tools:
            problems.append(f"{rel_path}: {tool}: missing version assertion")
    if "node" in pin_tools and "node" not in assertion_tools:
        msg = f"{rel_path}: node: missing version assertion"
        if msg not in problems:
            problems.append(msg)
    return problems


def parse_env_file(
    path: Path, rel_path: str
) -> tuple[dict[str, tuple[int, str, str]], list[str]]:
    """Parses NAME=value lines and preceding renovate comment from authority env file."""
    if not path.is_file():
        return {}, [f"{rel_path}:1: env: file does not exist"]
    env_vars: dict[str, tuple[int, str, str]] = {}
    problems: list[str] = []
    lines = read_bounded_lines(path)
    for idx, raw_line in enumerate(lines):
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        m = ENV_LINE_RE.match(line)
        if m:
            name, val = m.group(1), m.group(2).strip().strip("'\"")
            if name in env_vars:
                prev_line = env_vars[name][0]
                problems.append(
                    f"{rel_path}:{idx + 1}: {name}: duplicate assignment (first at line {prev_line})"
                )
                continue
            comment = ""
            if idx > 0 and lines[idx - 1].strip().startswith("#"):
                comment = lines[idx - 1].strip()
            env_vars[name] = (idx + 1, val, comment)
    return env_vars, problems


def parse_workflow_level_env(
    lines: list[str],
) -> dict[str, tuple[int, str, str]]:
    """Extracts workflow-level env block declarations and preceding comments."""
    env_vars: dict[str, tuple[int, str, str]] = {}
    in_env = False
    for idx, raw_line in enumerate(lines):
        line = raw_line.rstrip()
        if not in_env:
            if re.match(r"^env:\s*(?:#.*)?$", line):
                in_env = True
            continue
        if line and not line.startswith(" ") and not line.startswith("\t"):
            break
        m = re.match(r"^\s+([A-Z0-9_]+):\s*(.*)$", line)
        if m:
            name = m.group(1)
            val = m.group(2).strip().strip("'\"")
            comment = ""
            if idx > 0 and lines[idx - 1].strip().startswith("#"):
                comment = lines[idx - 1].strip()
            env_vars[name] = (idx + 1, val, comment)
    return env_vars


def check_group_b_workflow(
    rel_path: str,
    lines: list[str],
    pins: list[Pin],
    authority_env: dict[str, tuple[int, str, str]],
    node_authority: str,
) -> tuple[dict[str, tuple[int, str, str]], list[str]]:
    """Rule c: group B files declare matching workflow-level env with renovate comments."""
    problems: list[str] = []
    wf_env = parse_workflow_level_env(lines)
    installed_tools = {p.tool for p in pins}
    for tool in installed_tools:
        var_name = TOOL_ENV_VARS[tool]
        if tool == "node":
            expected_val = node_authority.strip()
            expected_comment = NODE_RENOVATE_COMMENT
        else:
            if var_name not in authority_env:
                problems.append(f"{rel_path}: authority env missing {var_name}")
                continue
            expected_val = authority_env[var_name][1]
            expected_comment = authority_env[var_name][2]

        if var_name not in wf_env:
            problems.append(f"{rel_path}: missing workflow-level env {var_name}")
            continue

        line_no, val, comment = wf_env[var_name]
        if val != expected_val:
            problems.append(
                f"{rel_path}:{line_no}: {var_name}: env value {val} != authority {expected_val}"
            )
        if comment != expected_comment:
            problems.append(
                f"{rel_path}:{line_no}: {var_name}: missing or incorrect renovate comment"
            )
    return wf_env, problems


def check_group_a_env_redeclarations(
    rel_path: str,
    lines: list[str],
) -> list[str]:
    """Rule d: group A files do not re-declare env vars."""
    problems: list[str] = []
    in_env = False
    env_indent = -1
    for idx, raw_line in enumerate(lines):
        line = raw_line.rstrip()
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        indent = len(line) - len(line.lstrip())
        if in_env:
            if indent <= env_indent and not stripped.startswith("-"):
                in_env = False
            else:
                m = re.match(r"^([A-Z0-9_]+):", stripped)
                if m and m.group(1) in ALL_RELEASE_VARS:
                    problems.append(
                        f"{rel_path}:{idx + 1}: group A workflow re-declares env {m.group(1)}"
                    )
        if stripped.startswith("env:"):
            in_env = True
            env_indent = indent
    return problems


def check_group_a_export_before_install(
    rel_path: str,
    lines: list[str],
) -> list[str]:
    """Rule d: group A workflows run export script before tool installs."""
    problems: list[str] = []
    in_jobs = False
    current_job = ""
    has_exported = False
    for idx, raw_line in enumerate(lines):
        line = raw_line.rstrip()
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        indent = len(line) - len(line.lstrip())
        if not in_jobs:
            if re.match(r"^jobs:\s*$", line):
                in_jobs = True
            continue
        if indent == 2 and line.endswith(":") and not stripped.startswith("-"):
            current_job = stripped.rstrip(":")
            has_exported = False
            continue
        if "scripts/ci/export-release-tool-pins.sh" in line:
            has_exported = True
            continue
        m = ACTION_LINE_RE.search(line)
        if m and m.group(1) in TOOL_ACTIONS:
            action = m.group(1)
            tool = TOOL_ACTIONS[action][0]
            if not has_exported:
                problems.append(
                    f"{rel_path}:{idx + 1}: job {current_job} installs {tool} before running scripts/ci/export-release-tool-pins.sh"
                )
    return problems


def check_group_a_workflow(
    rel_path: str,
    lines: list[str],
) -> list[str]:
    """Rule d: group A files do not re-declare env vars and run export script before tool installs."""
    return (
        check_group_a_env_redeclarations(rel_path, lines)
        + check_group_a_export_before_install(rel_path, lines)
    )


def load_renovate_custom_managers(
    root: Path,
) -> tuple[list[dict[str, object]], list[str]]:
    """Loads and validates customManagers from renovate.json."""
    renovate_path = root / "renovate.json"
    if not renovate_path.is_file():
        return [], ["renovate.json: file does not exist"]
    try:
        config = json.loads(renovate_path.read_text(encoding="utf-8"))
    except Exception as exc:
        return [], [f"renovate.json: invalid JSON: {exc}"]
    custom_managers = config.get("customManagers", [])
    if not isinstance(custom_managers, list):
        return [], ["renovate.json: customManagers is not a list"]
    return custom_managers, []


def collect_file_matched_vars(
    content: str,
    rel_path: str,
    env_vars: dict[str, tuple[int, str, str]],
    custom_managers: list[dict[str, object]],
) -> set[str]:
    """Collects environment variables matched by custom regex managers."""
    matched_vars: set[str] = set()
    for manager in custom_managers:
        if not isinstance(manager, dict) or manager.get("customType") != "regex":
            continue
        file_patterns = manager.get("managerFilePatterns", [])
        match_strings = manager.get("matchStrings", [])
        if not any(matches_file(str(p), rel_path) for p in file_patterns):
            continue
        for ms in match_strings:
            rx = compile_js_regex(str(ms))
            for m in rx.finditer(content):
                matched_text = m.group(0)
                for var_name in env_vars:
                    if f"{var_name}:" in matched_text:
                        matched_vars.add(var_name)
    return matched_vars


def check_group_b_renovate_coverage(
    root: Path,
    group_b_envs: dict[str, dict[str, tuple[int, str, str]]],
) -> list[str]:
    """Rule e: every group B env declaration is matched by a renovate.json customManager."""
    custom_managers, problems = load_renovate_custom_managers(root)
    if problems:
        return problems

    for rel_path, env_vars in group_b_envs.items():
        file_path = root / rel_path
        if not file_path.is_file():
            continue
        content = file_path.read_text(encoding="utf-8")
        matched_vars = collect_file_matched_vars(
            content, rel_path, env_vars, custom_managers
        )
        for var_name, (line_no, _, _) in env_vars.items():
            # Only release tool environment variables require Renovate manager coverage.
            if var_name in ALL_RELEASE_VARS and var_name not in matched_vars:
                problems.append(
                    f"{rel_path}:{line_no}: {var_name}: env copy not matched by renovate.json customManager"
                )
    return problems


def check_env_pins(
    root: Path,
    authority_env: dict[str, tuple[int, str, str]],
    all_pins: list[Pin],
) -> list[str]:
    """Verifies that every tool declared in authority env has install pins in workflows."""
    problems: list[str] = []
    rel_env = "tools/tool-versions.env"
    pins_by_tool: dict[str, list[Pin]] = {}
    for p in all_pins:
        pins_by_tool.setdefault(p.tool, []).append(p)
    for tool, var_name in ENV_TOOL_VARS.items():
        if var_name not in authority_env:
            problems.append(f"{rel_env}:1: {tool}: missing {var_name}")
            continue
        env_line = authority_env[var_name][0]
        tool_pins = pins_by_tool.get(tool, [])
        if not tool_pins:
            problems.append(
                f"{rel_env}:{env_line}: {tool}: no workflow install pin to compare"
            )
    return problems


def check_release_tool_pins(root: Path) -> list[str]:
    """Coordinates validation across workflows, authority env, node version, and renovate.json."""
    problems: list[str] = []
    all_pins: list[Pin] = []
    pins_by_file: dict[str, list[Pin]] = {}
    group_b_envs: dict[str, dict[str, tuple[int, str, str]]] = {}

    rel_env = "tools/tool-versions.env"
    authority_env, env_problems = parse_env_file(root / rel_env, rel_env)
    problems.extend(env_problems)

    node_file = root / ".node-version"
    if not node_file.is_file():
        problems.append(".node-version: file does not exist")
        node_authority = ""
    else:
        node_authority = node_file.read_text(encoding="utf-8").strip()

    for rel_path in WORKFLOW_FILES[:MAX_FILES]:
        path = root / rel_path
        pins, assertions, lines, parse_problems = parse_workflow_file(path, rel_path)
        problems.extend(parse_problems)
        pins_by_file[rel_path] = pins
        all_pins.extend(pins)
        problems.extend(
            check_workflow_node_and_required_tools(rel_path, pins, assertions)
        )
        if rel_path in GROUP_A_FILES:
            problems.extend(check_group_a_workflow(rel_path, lines))
        elif rel_path in GROUP_B_FILES:
            wf_env, gb_problems = check_group_b_workflow(
                rel_path, lines, pins, authority_env, node_authority
            )
            group_b_envs[rel_path] = wf_env
            problems.extend(gb_problems)

    problems.extend(check_scope_coverage(WORKFLOW_FILES, pins_by_file))
    problems.extend(check_env_pins(root, authority_env, all_pins))
    problems.extend(check_group_b_renovate_coverage(root, group_b_envs))
    return problems


VALID_FIXTURE_ENV = """\
# renovate: datasource=github-releases depName=docker/buildx versioning=semver
BUILDX_VERSION=0.38.1
# renovate: datasource=github-releases depName=sigstore/cosign versioning=semver
COSIGN_VERSION=3.1.4
# renovate: datasource=github-releases depName=anchore/syft versioning=semver
SYFT_VERSION=1.56.0
# renovate: datasource=github-releases depName=goreleaser/goreleaser versioning=semver
GORELEASER_VERSION=2.19.0
"""

VALID_FIXTURE_GROUP_A = r"""
name: Test Group A
on: push
jobs:
  test:
    runs-on: ubuntu-26.04
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7
      - name: Read release tool pins
        shell: bash
        run: bash scripts/ci/export-release-tool-pins.sh
      - name: Install cosign
        uses: sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6 # v4.1.2
        with:
          cosign-release: v${{ env.COSIGN_VERSION }}
      - name: Install syft
        uses: anchore/sbom-action/download-syft@66cbf4bc1f1c0d2edc94016e65bc221b6bb0ad6c # v0.24.3
        with:
          syft-version: v${{ env.SYFT_VERSION }}
      - name: Install goreleaser
        uses: goreleaser/goreleaser-action@a4f02ab5355a589de60d917a659ce617ed19b3de # v7.3.0
        with:
          version: v${{ env.GORELEASER_VERSION }}
      - name: Install buildx
        uses: docker/setup-buildx-action@f87e5991a6d7451dcb8d9637bfbc97413f497069 # v4
        with:
          version: v${{ env.BUILDX_VERSION }}
      - name: Install node
        uses: actions/setup-node@949feb2413d6458794dcd2491c4babbbce0c15c1 # v7.1.0
        with:
          node-version: ${{ env.NODE_VERSION }}
      - name: Verify tools
        run: |
          buildx_version="$(docker buildx version)"; grep -Eq "^github.com/docker/buildx v${BUILDX_VERSION//./\\.}([[:space:]]|$)" <<<"$buildx_version"
          cosign_version="$(cosign version)"; grep -Eq "^GitVersion:[[:space:]]+v?${COSIGN_VERSION//./\\.}(\+dirty)?[[:space:]]*$" <<<"$cosign_version"
          syft_version="$(syft version)"; grep -Eq "Version:[[:space:]]+${SYFT_VERSION//./\\.}([[:space:]]|$)" <<<"$syft_version"
          goreleaser_version="$(goreleaser --version)"; grep -Eq "GitVersion:[[:space:]]+v?${GORELEASER_VERSION//./\\.}([[:space:]]|$)" <<<"$goreleaser_version"
          test "$(node --version)" = "v${NODE_VERSION}"
"""

VALID_FIXTURE_GROUP_B = r"""
name: Test Group B
on: workflow_call
permissions:
  contents: read

# Reusable workflow runs in caller checkouts, so pins live here; renovate.json moves them with tools/tool-versions.env.
env:
  # renovate: datasource=github-releases depName=docker/buildx versioning=semver
  BUILDX_VERSION: 0.38.1
  # renovate: datasource=github-releases depName=sigstore/cosign versioning=semver
  COSIGN_VERSION: 3.1.4
  # renovate: datasource=github-releases depName=anchore/syft versioning=semver
  SYFT_VERSION: 1.56.0
  # renovate: datasource=github-releases depName=goreleaser/goreleaser versioning=semver
  GORELEASER_VERSION: 2.19.0
  # renovate: datasource=node-version depName=node versioning=node
  NODE_VERSION: 24.22.2

jobs:
  test:
    runs-on: ubuntu-26.04
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7
      - name: Install cosign
        uses: sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6 # v4.1.2
        with:
          cosign-release: v${{ env.COSIGN_VERSION }}
      - name: Install syft
        uses: anchore/sbom-action/download-syft@66cbf4bc1f1c0d2edc94016e65bc221b6bb0ad6c # v0.24.3
        with:
          syft-version: v${{ env.SYFT_VERSION }}
      - name: Install goreleaser
        uses: goreleaser/goreleaser-action@a4f02ab5355a589de60d917a659ce617ed19b3de # v7.3.0
        with:
          version: v${{ env.GORELEASER_VERSION }}
      - name: Install buildx
        uses: docker/setup-buildx-action@f87e5991a6d7451dcb8d9637bfbc97413f497069 # v4
        with:
          version: v${{ env.BUILDX_VERSION }}
      - name: Install node
        uses: actions/setup-node@949feb2413d6458794dcd2491c4babbbce0c15c1 # v7.1.0
        with:
          node-version: ${{ env.NODE_VERSION }}
      - name: Verify tools
        run: |
          buildx_version="$(docker buildx version)"; grep -Eq "^github.com/docker/buildx v${BUILDX_VERSION//./\\.}([[:space:]]|$)" <<<"$buildx_version"
          cosign_version="$(cosign version)"; grep -Eq "^GitVersion:[[:space:]]+v?${COSIGN_VERSION//./\\.}(\+dirty)?[[:space:]]*$" <<<"$cosign_version"
          syft_version="$(syft version)"; grep -Eq "Version:[[:space:]]+${SYFT_VERSION//./\\.}([[:space:]]|$)" <<<"$syft_version"
          goreleaser_version="$(goreleaser --version)"; grep -Eq "GitVersion:[[:space:]]+v?${GORELEASER_VERSION//./\\.}([[:space:]]|$)" <<<"$goreleaser_version"
          test "$(node --version)" = "v${NODE_VERSION}"
"""


def create_valid_fixture(root: Path) -> None:
    """Populates temporary directory with valid release workflows, env file, and Renovate config."""
    workflows_dir = root / ".github/workflows"
    workflows_dir.mkdir(parents=True, exist_ok=True)
    tools_dir = root / "tools"
    tools_dir.mkdir(parents=True, exist_ok=True)
    (tools_dir / "tool-versions.env").write_text(VALID_FIXTURE_ENV, encoding="utf-8")
    (root / ".node-version").write_text("24.22.2\n", encoding="utf-8")
    (root / "renovate.json").write_text(
        (REPO_ROOT / "renovate.json").read_text(encoding="utf-8"), encoding="utf-8"
    )
    for name in WORKFLOW_FILES[:MAX_FILES]:
        target = root / name
        if name in GROUP_B_FILES:
            target.write_text(VALID_FIXTURE_GROUP_B, encoding="utf-8")
        else:
            target.write_text(VALID_FIXTURE_GROUP_A, encoding="utf-8")


def apply_renovate_bump(root: Path, config: dict, target_dep: str, new_val: str) -> None:
    """Emulates Renovate regex manager execution across matching files."""
    for m in config.get("customManagers", []):
        file_pats = m.get("managerFilePatterns", [])
        match_strings = m.get("matchStrings", [])
        for rel_path in [
            "tools/tool-versions.env",
            ".github/workflows/release-go.yml",
            ".github/workflows/rebuild-on-base.yml",
            ".github/workflows/verify-provenance.yml",
        ]:
            if not any(matches_file(p, rel_path) for p in file_pats):
                continue
            file_path = root / rel_path
            if not file_path.is_file():
                continue
            content = file_path.read_text(encoding="utf-8")
            for ms in match_strings:
                rx = compile_js_regex(ms)
                def replace_match(match: re.Match[str]) -> str:
                    """Replaces current value with new value when depName matches target."""
                    if match.groupdict().get("depName") == target_dep:
                        cur = match.groupdict().get("currentValue")
                        if cur:
                            return match.group(0).replace(cur, new_val)
                    return match.group(0)
                content = rx.sub(replace_match, content)
            file_path.write_text(content, encoding="utf-8")


class ReleaseToolPinsTest(unittest.TestCase):
    def test_planted_composite_pin_drift(self) -> None:
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            for name in COMPOSITE_ACTIONS:
                action = root / ".github/actions" / name / "action.yml"
                action.parent.mkdir(parents=True)
                action.write_text(
                    (REPO_ROOT / action.relative_to(root)).read_text(encoding="utf-8"),
                    encoding="utf-8",
                )
            relative = Path(".github/workflows/tiny-trainer-images.yml")
            (root / relative).write_text(
                (REPO_ROOT / relative).read_text(encoding="utf-8"), encoding="utf-8"
            )
            self.assertEqual(check_release_tool_pins(root), [])
            action = root / ".github/actions/setup-image-signing/action.yml"
            action.write_text(
                action.read_text(encoding="utf-8").replace(
                    "cosign-release: v${{ env.COSIGN_VERSION }}",
                    "cosign-release: v0.0.0",
                ),
                encoding="utf-8",
            )
            self.assert_planted_defect(
                check_release_tool_pins(root), "cosign: literal pin v0.0.0"
            )

    def test_composite_expansion_keeps_next_job(self) -> None:
        with tempfile.TemporaryDirectory() as tmp_str:
            workflow = Path(tmp_str) / "workflow.yml"
            following = "  next:\n    steps:\n      - run: echo next\n"
            workflow.write_text(
                "jobs:\n  publish:\n    steps:\n"
                "      - uses: ./.github/actions/setup-image-signing\n" + following,
                encoding="utf-8",
            )
            self.assertTrue(expand_workflow(workflow, REPO_ROOT).endswith(following))

    def test_planted_expansion_inventory_overflow(self) -> None:
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            for index in range(129):
                (root / f"{index}.yml").touch()
            with self.assertRaisesRegex(ValueError, "exceeds 128 files"):
                write_expanded_workflows(root, root / "expanded")

    def test_composite_scalar_inputs(self) -> None:
        self.assertEqual(scalar_values("  value: ''\n", 2), {"value": ""})
        self.assertEqual(
            scalar_values("  contexts: |\n    common=shared\n  next: value\n", 2),
            {"contexts": "common=shared\n", "next": "value"},
        )
        with self.assertRaisesRegex(ValueError, "duplicate composite scalar"):
            scalar_values("  pin: v1\n  pin: v2\n", 2)

    def test_composite_input_substitution(self) -> None:
        self.assertEqual(
            substitute_inputs(
                "      contexts: ${{ inputs.contexts }}\n",
                {"contexts": "common=shared\n"},
            ),
            "      contexts: |\n        common=shared\n",
        )
        with self.assertRaises(KeyError):
            substitute_inputs("      version: ${{ inputs.missing }}\n", {})

    def test_planted_conditional_composite_call(self) -> None:
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            workflow = root / "workflow.yml"
            workflow.write_text(
                "jobs:\n  publish:\n    steps:\n"
                "      - uses: ./.github/actions/setup-image-signing\n"
                "        if: false\n",
                encoding="utf-8",
            )
            with self.assertRaisesRegex(ValueError, "conditional composite call"):
                expand_workflow(workflow, REPO_ROOT)

    def test_planted_recursive_composite(self) -> None:
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            action = root / ".github/actions/setup-image-signing/action.yml"
            action.parent.mkdir(parents=True)
            action.write_text(
                "runs:\n  using: composite\n  steps:\n"
                "    - uses: ./.github/actions/setup-image-signing\n",
                encoding="utf-8",
            )
            workflow = root / "workflow.yml"
            workflow.write_text(
                "jobs:\n  publish:\n    steps:\n"
                "      - uses: ./.github/actions/setup-image-signing\n",
                encoding="utf-8",
            )
            with self.assertRaisesRegex(ValueError, "exceeds four levels"):
                expand_workflow(workflow, root)

    def assert_planted_defect(self, problems: list[str], expected: str) -> None:
        # The exact rule message must appear, so another rule's finding cannot satisfy the case.
        self.assertTrue(
            any(expected in p for p in problems),
            f"expected {expected!r} in {problems}",
        )

    def test_repository_pins_agree(self) -> None:
        # Verifies actual repository workflows and env pins agree.
        problems = check_release_tool_pins(REPO_ROOT)
        self.assertEqual(problems, [])

    def test_planted_assertion_mismatch_same_file(self) -> None:
        """Tests detection of literal assertion in workflow."""
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-tool-contract.yml"
            text = path.read_text(encoding="utf-8")
            text = text.replace(r"${BUILDX_VERSION//./\\.}", r"0\.37\.1")
            path.write_text(text, encoding="utf-8")
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(
                problems, "buildx: assertion contains literal version"
            )

    def test_planted_node_assertion_mismatch(self) -> None:
        """Tests detection of node assertion with literal version."""
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-go.yml"
            text = path.read_text(encoding="utf-8")
            text = text.replace(r"${NODE_VERSION}", "24.21.0")
            path.write_text(text, encoding="utf-8")
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(
                problems, "node: assertion contains literal version"
            )

    def test_planted_required_tool_missing_pin_and_assertion(self) -> None:
        """Tests detection when required tool install pin and assertion are missing."""
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-go.yml"
            text = path.read_text(encoding="utf-8")
            text = text.replace("actions/setup-node", "actions/setup-other")
            text = text.replace(NODE_ASSERTION_EXACT, "echo skip")
            path.write_text(text, encoding="utf-8")
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(problems, "missing install pin")

    def test_planted_missing_node_version_assertion(self) -> None:
        """Tests detection when node version assertion is missing despite install pin."""
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-tool-contract.yml"
            text = path.read_text(encoding="utf-8")
            text = text.replace(NODE_ASSERTION_EXACT, "echo skip")
            path.write_text(text, encoding="utf-8")
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(problems, "missing version assertion")

    def test_planted_commented_node_assertion(self) -> None:
        """Tests detection when node version assertion in release-go.yml is commented out."""
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-go.yml"
            text = path.read_text(encoding="utf-8")
            text = text.replace(NODE_ASSERTION_EXACT, f"# {NODE_ASSERTION_EXACT}")
            path.write_text(text, encoding="utf-8")
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(problems, "missing version assertion")

    def test_planted_node_assertion_outside_run(self) -> None:
        """Tests that a step name carrying the assertion text does not satisfy it."""
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-go.yml"
            text = path.read_text(encoding="utf-8")
            text = text.replace(NODE_ASSERTION_EXACT, "echo ok")
            text += f"      - name: {NODE_ASSERTION_EXACT}\n        run: echo ok\n"
            path.write_text(text, encoding="utf-8")
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(problems, "missing version assertion")

    def test_inline_run_node_assertion_valid(self) -> None:
        """Positive: an inline run value carries the assertion."""
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-go.yml"
            text = path.read_text(encoding="utf-8")
            text = text.replace(NODE_ASSERTION_EXACT, "echo ok")
            text += f"      - name: Verify node\n        run: {NODE_ASSERTION_EXACT}\n"
            path.write_text(text, encoding="utf-8")
            self.assertEqual(check_release_tool_pins(root), [])

    def test_planted_commented_node_install_pin(self) -> None:
        """Tests detection when setup-node uses action line in release-go.yml is commented out."""
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-go.yml"
            text = path.read_text(encoding="utf-8")
            text = text.replace("uses: actions/setup-node", "# uses: actions/setup-node")
            path.write_text(text, encoding="utf-8")
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(problems, "missing install pin")

    def test_planted_dash_commented_node_install_pin(self) -> None:
        # Tests detection when the setup-node step is a dash-prefixed commented uses line.
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-go.yml"
            text = path.read_text(encoding="utf-8")
            step = "      - name: Install node\n        uses: actions/setup-node"
            self.assertIn(step, text)
            text = text.replace(step, "      - # uses: actions/setup-node")
            path.write_text(text, encoding="utf-8")
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(problems, "missing install pin")

    def test_planted_node_assertion_unanchored(self) -> None:
        """Tests detection of unanchored or invalid node version assertion."""
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-go.yml"
            text = path.read_text(encoding="utf-8")
            text = text.replace(NODE_ASSERTION_EXACT, "node --version | grep v24.22.2")
            path.write_text(text, encoding="utf-8")
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(problems, "node: assertion must equal")

    def test_planted_node_assertion_trailing_or_true(self) -> None:
        """Tests detection of node assertion with trailing command bypass."""
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-go.yml"
            text = path.read_text(encoding="utf-8")
            text = text.replace(NODE_ASSERTION_EXACT, f"{NODE_ASSERTION_EXACT} || true")
            path.write_text(text, encoding="utf-8")
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(problems, "node: assertion must equal")

    def test_planted_pins_differ_across_files(self) -> None:
        """Tests detection of literal pin inputs differing from expected variable expression."""
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-build.yml"
            content = path.read_text(encoding="utf-8")
            content = content.replace(
                "syft-version: v${{ env.SYFT_VERSION }}", "syft-version: v1.55.0"
            )
            path.write_text(content, encoding="utf-8")
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(problems, "syft: literal pin v1.55.0")

    def test_planted_env_differs(self) -> None:
        """Tests detection of tools/tool-versions.env differing from workflow env pin."""
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / "tools/tool-versions.env"
            path.write_text(
                path.read_text(encoding="utf-8").replace(
                    "COSIGN_VERSION=3.1.4", "COSIGN_VERSION=3.1.2"
                ),
                encoding="utf-8",
            )
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(
                problems, "COSIGN_VERSION: env value 3.1.4 != authority 3.1.2"
            )

    def test_planted_duplicate_env_key(self) -> None:
        # Tests refusal of duplicate key assignment in tools/tool-versions.env.
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / "tools/tool-versions.env"
            path.write_text(
                path.read_text(encoding="utf-8") + "\nCOSIGN_VERSION=3.1.4\n",
                encoding="utf-8",
            )
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(problems, "duplicate assignment")

    def test_planted_action_without_version_key(self) -> None:
        """Tests detection of installer action lacking required version key."""
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-build.yml"
            path.write_text(
                path.read_text(encoding="utf-8").replace(
                    "cosign-release: v${{ env.COSIGN_VERSION }}",
                    "missing: v${{ env.COSIGN_VERSION }}",
                ),
                encoding="utf-8",
            )
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(
                problems, "sigstore/cosign-installer has no cosign-release pin"
            )

    def test_planted_assertion_without_version(self) -> None:
        """Tests detection of assertion regex without variable reference."""
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-build.yml"
            path.write_text(
                path.read_text(encoding="utf-8").replace(
                    r"${COSIGN_VERSION//./\\.}", "unparseable"
                ),
                encoding="utf-8",
            )
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(
                problems, "cosign: assertion does not reference"
            )

    def test_planted_empty_file(self) -> None:
        # Tests refusal of workflow file lacking any install pins.
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            (root / ".github/workflows/release-build.yml").write_text(
                "", encoding="utf-8"
            )
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(problems, "yielded no install pins")

    def test_planted_tool_without_any_pin(self) -> None:
        # An env entry whose tool no workflow installs must not pass unchecked.
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            for name in WORKFLOW_FILES:
                path = root / name
                text = path.read_text(encoding="utf-8")
                path.write_text(
                    text.replace(
                        "goreleaser/goreleaser-action", "example/other-action"
                    ),
                    encoding="utf-8",
                )
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(
                problems, "goreleaser: no workflow install pin to compare"
            )

    def test_file_over_line_bound_fails(self) -> None:
        # Boundary: a file one line past the bound is refused, not truncated.
        with tempfile.TemporaryDirectory() as tmp_str:
            path = Path(tmp_str) / "long.yml"
            path.write_text("x\n" * MAX_LINES, encoding="utf-8")
            self.assertEqual(len(read_bounded_lines(path)), MAX_LINES)
            path.write_text("x\n" * (MAX_LINES + 1), encoding="utf-8")
            with self.assertRaises(ValueError):
                read_bounded_lines(path)

    def test_planted_input_only_bump_fails(self) -> None:
        """Tests detection of literal pin input edit bumping tool version directly."""
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-go.yml"
            path.write_text(
                path.read_text(encoding="utf-8").replace(
                    "version: v${{ env.BUILDX_VERSION }}", "version: v0.38.2"
                ),
                encoding="utf-8",
            )
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(problems, "literal pin v0.38.2")

    def test_planted_group_b_env_drift_fails(self) -> None:
        """Tests detection when Group B workflow env pin drifts from authority."""
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-go.yml"
            path.write_text(
                path.read_text(encoding="utf-8").replace(
                    "COSIGN_VERSION: 3.1.4", "COSIGN_VERSION: 3.1.5"
                ),
                encoding="utf-8",
            )
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(
                problems, "COSIGN_VERSION: env value 3.1.5 != authority 3.1.4"
            )

    def test_planted_group_b_env_missing_renovate_comment_fails(self) -> None:
        """Tests detection when Group B workflow env pin lacks authority renovate comment."""
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-go.yml"
            text = path.read_text(encoding="utf-8")
            text = text.replace(
                "  # renovate: datasource=github-releases depName=sigstore/cosign versioning=semver\n",
                "",
            )
            path.write_text(text, encoding="utf-8")
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(
                problems, "COSIGN_VERSION: missing or incorrect renovate comment"
            )

    def test_planted_assertion_literal_fails(self) -> None:
        """Tests detection when an assertion contains a literal version."""
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-build.yml"
            text = path.read_text(encoding="utf-8")
            text = text.replace(
                r"${COSIGN_VERSION//./\\.}",
                r"3\.1\.4",
            )
            path.write_text(text, encoding="utf-8")
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(problems, "assertion contains literal version")

    def test_planted_group_a_env_redeclaration_fails(self) -> None:
        """Tests that re-declaring release env vars in a group A workflow fails."""
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-build.yml"
            text = path.read_text(encoding="utf-8")
            text = text.replace(
                "    steps:\n",
                "    env:\n      COSIGN_VERSION: 3.1.4\n    steps:\n",
            )
            path.write_text(text, encoding="utf-8")
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(problems, "group A workflow re-declares env")

    def test_planted_group_b_missing_env_fails(self) -> None:
        """Tests that missing workflow-level env in a group B workflow fails."""
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-go.yml"
            text = path.read_text(encoding="utf-8")
            text = text.replace(
                "  # renovate: datasource=github-releases depName=sigstore/cosign versioning=semver\n  COSIGN_VERSION: 3.1.4\n",
                "",
            )
            path.write_text(text, encoding="utf-8")
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(problems, "missing workflow-level env")

    def test_planted_export_after_install_fails(self) -> None:
        """Tests that installing tools before export script in a group A workflow fails."""
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-build.yml"
            text = path.read_text(encoding="utf-8")
            export_step = (
                "      - name: Read release tool pins\n"
                "        shell: bash\n"
                "        run: bash scripts/ci/export-release-tool-pins.sh\n"
            )
            install_step = (
                "      - name: Install cosign\n"
                "        uses: sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6 # v4.1.2\n"
                "        with:\n"
                "          cosign-release: v${{ env.COSIGN_VERSION }}\n"
            )
            text = text.replace(export_step, "").replace(
                install_step, install_step + export_step
            )
            path.write_text(text, encoding="utf-8")
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(
                problems,
                "installs cosign before running scripts/ci/export-release-tool-pins.sh",
            )

    def test_planted_renovate_invalid_json_fails(self) -> None:
        """Tests that invalid JSON in renovate.json fails validation."""
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            (root / "renovate.json").write_text("{ invalid json", encoding="utf-8")
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(problems, "renovate.json: invalid JSON:")

    def test_planted_renovate_custom_managers_not_list_fails(self) -> None:
        """Tests that non-list customManagers in renovate.json fails validation."""
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            (root / "renovate.json").write_text(
                json.dumps({"customManagers": "not-a-list"}), encoding="utf-8"
            )
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(
                problems, "renovate.json: customManagers is not a list"
            )

    def test_renovate_shaped_bump_passes(self) -> None:
        """Tests that a coordinated Renovate bump across authority and group B workflows succeeds."""
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            config = json.loads((root / "renovate.json").read_text(encoding="utf-8"))
            apply_renovate_bump(root, config, "sigstore/cosign", "3.1.5")
            env_text = (root / "tools/tool-versions.env").read_text(encoding="utf-8")
            self.assertIn("COSIGN_VERSION=3.1.5", env_text)
            for name in GROUP_B_FILES:
                content = (root / name).read_text(encoding="utf-8")
                self.assertIn("COSIGN_VERSION: 3.1.5", content)
                self.assertNotIn("COSIGN_VERSION: 3.1.4", content)
            self.assertEqual(check_release_tool_pins(root), [])

    def test_renovate_shaped_bump_partial_coverage_fails(self) -> None:
        """Tests that excluding a group B file from Renovate patterns leaves copy behind and fails."""
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            config = json.loads((root / "renovate.json").read_text(encoding="utf-8"))
            for m in config.get("customManagers", []):
                pats = m.get("managerFilePatterns", [])
                m["managerFilePatterns"] = [p for p in pats if "verify-provenance" not in p]
            (root / "renovate.json").write_text(json.dumps(config), encoding="utf-8")
            apply_renovate_bump(root, config, "sigstore/cosign", "3.1.5")
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(
                problems, "env copy not matched by renovate.json customManager"
            )

    def test_group_b_unrelated_env_var_passes(self) -> None:
        """Tests that unrelated workflow-level env declarations in group B workflows pass."""
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / GROUP_B_FILES[0]
            text = path.read_text(encoding="utf-8")
            text = text.replace("env:\n", 'env:\n  CGO_ENABLED: "0"\n', 1)
            self.assertIn('CGO_ENABLED: "0"', text)
            path.write_text(text, encoding="utf-8")
            self.assertEqual(check_release_tool_pins(root), [])


if __name__ == "__main__":
    unittest.main()
