#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Regression tests proving release workflow tool pins agree with assertions."""

from __future__ import annotations

import re
import tempfile
import unittest
from pathlib import Path
from typing import NamedTuple

REPO_ROOT = Path(__file__).resolve().parents[2]

WORKFLOW_FILES: tuple[str, ...] = (
    ".github/workflows/release-build.yml",
    ".github/workflows/release-go.yml",
    ".github/workflows/release-tool-contract.yml",
    ".github/workflows/rebuild-on-base.yml",
    ".github/workflows/tiny-litert-sidecar-image.yml",
    ".github/workflows/tiny-trainer-images.yml",
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

ENV_TOOL_VARS: dict[str, str] = {
    "cosign": "COSIGN_VERSION",
    "syft": "SYFT_VERSION",
    "goreleaser": "GORELEASER_VERSION",
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
    "node": re.compile(
        r'node_version="\$\(node\s+--version\)";\s*grep\s+-Eq\s+.*<<<"\$node_version"'
    ),
}

NODE_ANCHORED_RE = re.compile(
    r"""grep\s+-Eq\s+(['"])\^v\d+\\\.\d+\\\.\d+\$\1\s+<<<"\$node_version"\s*$"""
)

ACTION_LINE_RE = re.compile(r"^\s*(?:-\s+)?uses:\s+([\w/-]+)@[0-9a-f]{40}\b")
VERSION_RE = re.compile(r"(\d+)\\\.(\d+)\\\.(\d+)")
ENV_LINE_RE = re.compile(r"^([A-Z0-9_]+)=(.*)$")
RUN_INLINE_RE = re.compile(r"^(?P<pre>\s*(?:-\s+)?)run:\s+(?P<cmd>[^|>\s].*)$")
RUN_BLOCK_RE = re.compile(r"^(?P<pre>\s*(?:-\s+)?)run:\s*[|>][-+0-9]*\s*(?:#.*)?$")
MAX_FILES = 16
MAX_LINES = 4096
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


def read_bounded_lines(path: Path) -> list[str]:
    # A file past the bound fails instead of being checked only in part.
    lines: list[str] = []
    with path.open("r", encoding="utf-8") as f:
        for idx, line in enumerate(f):
            if idx >= MAX_LINES:
                raise ValueError(f"{path}: more than {MAX_LINES} lines")
            lines.append(line)
    return lines


def parse_action_line(
    idx: int,
    line: str,
    lines: list[str],
    total: int,
    rel_path: str,
    pins: list[Pin],
    problems: list[str],
) -> None:
    # Extracts action version pin within bounded lookahead window.
    if is_comment_line(line):
        return
    match = ACTION_LINE_RE.search(line)
    if not match:
        return
    action = match.group(1)
    if action not in TOOL_ACTIONS:
        return
    tool, key = TOOL_ACTIONS[action]
    pin_re = re.compile(rf"^\s*{re.escape(key)}:\s+['\"]?v?(\d+\.\d+\.\d+)['\"]?\s*$")
    end = min(total, idx + 1 + LOOKAHEAD_LINES)
    for look_idx in range(idx + 1, end):
        if is_comment_line(lines[look_idx]):
            continue
        km = pin_re.search(lines[look_idx])
        if km:
            pins.append(Pin(rel_path, look_idx + 1, tool, km.group(1)))
            return
    problems.append(f"{rel_path}:{idx + 1}: {action} has no {key} pin")


def parse_assertion_line(
    idx: int,
    line: str,
    rel_path: str,
    assertions: list[Assertion],
    problems: list[str],
) -> None:
    # Extracts assertion version from tool grep regex.
    if is_comment_line(line):
        return
    line = line.rstrip()
    for tool, pattern in ASSERTION_PATTERNS.items():
        if not pattern.search(line):
            continue
        if tool == "node" and not NODE_ANCHORED_RE.search(line):
            problems.append(f"{rel_path}:{idx + 1}: node: assertion not anchored")
        vm = VERSION_RE.search(line)
        if vm:
            ver = f"{vm.group(1)}.{vm.group(2)}.{vm.group(3)}"
            assertions.append(Assertion(rel_path, idx + 1, tool, ver))
        else:
            problems.append(
                f"{rel_path}:{idx + 1}: {tool}: assertion regex yielded no version"
            )
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
) -> tuple[list[Pin], list[Assertion], list[str]]:
    # Parses action pins and assertions in single workflow pass.
    if not path.is_file():
        return [], [], [f"{rel_path}:1: tool: file does not exist"]
    lines = read_bounded_lines(path)
    pins: list[Pin] = []
    assertions: list[Assertion] = []
    problems: list[str] = []
    total = len(lines)
    for idx, line in enumerate(lines):
        if is_comment_line(line):
            continue
        parse_action_line(idx, line, lines, total, rel_path, pins, problems)
    for idx, text in collect_run_lines(lines):
        parse_assertion_line(idx, text, rel_path, assertions, problems)
    return pins, assertions, problems


def check_file_assertions(
    pins: list[Pin],
    assertions: list[Assertion],
) -> list[str]:
    # Rule a: assertion equals install pin in same file.
    problems: list[str] = []
    pins_by_tool: dict[str, list[Pin]] = {}
    for p in pins:
        pins_by_tool.setdefault(p.tool, []).append(p)
    for a in assertions:
        if a.tool not in pins_by_tool:
            problems.append(
                f"{a.file}:{a.line}: {a.tool}: assertion {a.version} has no install pin in this file"
            )
            continue
        problems.extend(
            f"{a.file}:{a.line}: {a.tool}: assertion {a.version} != pin {p.version}"
            for p in pins_by_tool[a.tool]
            if a.version != p.version
        )
    return problems


def check_pins_equal_across_files(all_pins: list[Pin]) -> list[str]:
    # Rule b: across all five files, all pins of T are equal.
    problems: list[str] = []
    pins_by_tool: dict[str, list[Pin]] = {}
    for p in all_pins:
        pins_by_tool.setdefault(p.tool, []).append(p)
    for tool, pins in pins_by_tool.items():
        if not pins:
            continue
        canonical = pins[0]
        problems.extend(
            f"{p.file}:{p.line}: {tool}: pin {p.version} != pin {canonical.version}"
            for p in pins[1:]
            if p.version != canonical.version
        )
    return problems


def parse_env_file(
    path: Path, rel_path: str
) -> tuple[dict[str, tuple[int, str]], list[str]]:
    # Parses NAME=value lines from tool-versions environment file.
    if not path.is_file():
        return {}, [f"{rel_path}:1: env: file does not exist"]
    env_vars: dict[str, tuple[int, str]] = {}
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
            env_vars[name] = (idx + 1, val)
    return env_vars, problems


def check_env_pins(root: Path, all_pins: list[Pin]) -> list[str]:
    # Rule c: tools/tool-versions.env agrees with workflow pins.
    problems: list[str] = []
    rel_env = "tools/tool-versions.env"
    env_vars, env_problems = parse_env_file(root / rel_env, rel_env)
    problems.extend(env_problems)
    pins_by_tool: dict[str, list[Pin]] = {}
    for p in all_pins:
        pins_by_tool.setdefault(p.tool, []).append(p)
    # Buildx and node have no env entry in tools/tool-versions.env: skip rule c for them.
    for tool, var_name in ENV_TOOL_VARS.items():
        if var_name not in env_vars:
            problems.append(f"{rel_env}:1: {tool}: missing {var_name}")
            continue
        env_line, env_raw = env_vars[var_name]
        env_ver = env_raw.lstrip("v")
        tool_pins = pins_by_tool.get(tool, [])
        if not tool_pins:
            problems.append(
                f"{rel_env}:{env_line}: {tool}: no workflow install pin to compare"
            )
            continue
        expected_ver = tool_pins[0].version
        if env_ver != expected_ver:
            problems.append(
                f"{rel_env}:{env_line}: {tool}: env {env_ver} != workflow pin {expected_ver}"
            )
    return problems


def check_scope_coverage(
    workflow_files: tuple[str, ...],
    pins_by_file: dict[str, list[Pin]],
) -> list[str]:
    # Rule d: every file in scope yields at least one install pin.
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
    # Enforces required tool pins/assertions and node pin-assertion pairing.
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


def check_release_tool_pins(root: Path) -> list[str]:
    # Coordinates rules a-d across workflows and tool-versions.env.
    problems: list[str] = []
    all_pins: list[Pin] = []
    pins_by_file: dict[str, list[Pin]] = {}
    for rel_path in WORKFLOW_FILES[:MAX_FILES]:
        path = root / rel_path
        pins, assertions, parse_problems = parse_workflow_file(path, rel_path)
        problems.extend(parse_problems)
        pins_by_file[rel_path] = pins
        all_pins.extend(pins)
        problems.extend(check_file_assertions(pins, assertions))
        problems.extend(
            check_workflow_node_and_required_tools(rel_path, pins, assertions)
        )
    problems.extend(check_scope_coverage(WORKFLOW_FILES, pins_by_file))
    problems.extend(check_pins_equal_across_files(all_pins))
    problems.extend(check_env_pins(root, all_pins))
    return problems


VALID_FIXTURE_ENV = """\
COSIGN_VERSION=3.1.4
SYFT_VERSION=1.56.0
GORELEASER_VERSION=2.19.0
"""

VALID_FIXTURE_WORKFLOW = r"""\
      - name: Install cosign
        uses: sigstore/cosign-installer@6f9f17788090df1f26f669e9d70d6ae9567deba6 # v4.1.2
        with:
          cosign-release: v3.1.4
      - name: Install syft
        uses: anchore/sbom-action/download-syft@66cbf4bc1f1c0d2edc94016e65bc221b6bb0ad6c # v0.24.3
        with:
          syft-version: v1.56.0
      - name: Install goreleaser
        uses: goreleaser/goreleaser-action@f06c13b6b1a9625abc9e6e439d9c05a8f2190e94 # v7.2.3
        with:
          version: v2.19.0
      - name: Install buildx
        uses: docker/setup-buildx-action@f87e5991a6d7451dcb8d9637bfbc97413f497069 # v4
        with:
          version: v0.38.0
      - name: Install node
        uses: actions/setup-node@949feb2413d6458794dcd2491c4babbbce0c15c1 # v7.1.0
        with:
          node-version: 24.22.2
      - name: Verify tools
        run: |
          cosign_version="$(cosign version)"; grep -Eq '^GitVersion:[[:space:]]+v?3\.1\.4(\+dirty)?[[:space:]]*$' <<<"$cosign_version"
          syft_version="$(syft version)"; grep -Eq 'Version:[[:space:]]+1\.56\.0([[:space:]]|$)' <<<"$syft_version"
          goreleaser_version="$(goreleaser --version)"; grep -Eq 'GitVersion:[[:space:]]+v?2\.19\.0([[:space:]]|$)' <<<"$goreleaser_version"
          buildx_version="$(docker buildx version)"; grep -Eq '^github.com/docker/buildx v0\.38\.0([[:space:]]|$)' <<<"$buildx_version"
          node_version="$(node --version)"; grep -Eq '^v24\.22\.2$' <<<"$node_version"
"""


def create_valid_fixture(root: Path) -> None:
    # Populates temporary directory with valid release workflows and env file.
    workflows_dir = root / ".github/workflows"
    workflows_dir.mkdir(parents=True, exist_ok=True)
    tools_dir = root / "tools"
    tools_dir.mkdir(parents=True, exist_ok=True)
    (tools_dir / "tool-versions.env").write_text(VALID_FIXTURE_ENV, encoding="utf-8")
    for name in WORKFLOW_FILES[:MAX_FILES]:
        (root / name).write_text(VALID_FIXTURE_WORKFLOW, encoding="utf-8")


class ReleaseToolPinsTest(unittest.TestCase):
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
        # Tests detection of assertion differing from pin in same file.
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-tool-contract.yml"
            path.write_text(
                path.read_text(encoding="utf-8").replace("v0\\.38\\.0", "v0\\.37\\.1"),
                encoding="utf-8",
            )
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(
                problems, "buildx: assertion 0.37.1 != pin 0.38.0"
            )

    def test_planted_node_assertion_mismatch(self) -> None:
        # Tests detection of node assertion differing from pin in same file.
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-go.yml"
            path.write_text(
                path.read_text(encoding="utf-8").replace("24\\.22\\.2", "24\\.21\\.0"),
                encoding="utf-8",
            )
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(
                problems, "node: assertion 24.21.0 != pin 24.22.2"
            )

    def test_planted_required_tool_missing_pin_and_assertion(self) -> None:
        # Tests detection when required tool install pin and assertion are missing.
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-go.yml"
            text = path.read_text(encoding="utf-8")
            text = text.replace("actions/setup-node", "actions/setup-other")
            text = text.replace(
                'node_version="$(node --version)"; grep -Eq \'^v24\\.22\\.2$\' <<<"$node_version"',
                "echo skip",
            )
            path.write_text(text, encoding="utf-8")
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(problems, "missing install pin")

    def test_planted_missing_node_version_assertion(self) -> None:
        # Tests detection when node version assertion is missing despite install pin.
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-tool-contract.yml"
            text = path.read_text(encoding="utf-8")
            text = text.replace(
                'node_version="$(node --version)"; grep -Eq \'^v24\\.22\\.2$\' <<<"$node_version"',
                "echo skip",
            )
            path.write_text(text, encoding="utf-8")
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(problems, "missing version assertion")

    def test_planted_commented_node_assertion(self) -> None:
        # Tests detection when node version assertion in release-go.yml is commented out.
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-go.yml"
            text = path.read_text(encoding="utf-8")
            text = text.replace(
                'node_version="$(node --version)"; grep -Eq \'^v24\\.22\\.2$\' <<<"$node_version"',
                '# node_version="$(node --version)"; grep -Eq \'^v24\\.22\\.2$\' <<<"$node_version"',
            )
            path.write_text(text, encoding="utf-8")
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(problems, "missing version assertion")

    def test_planted_node_assertion_outside_run(self) -> None:
        # Tests that a step name carrying the assertion text does not satisfy it.
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-go.yml"
            text = path.read_text(encoding="utf-8")
            text = text.replace(
                'node_version="$(node --version)"; grep -Eq \'^v24\\.22\\.2$\' <<<"$node_version"',
                "echo ok",
            )
            text += (
                "      - name: node_version=\"$(node --version)\"; grep -Eq '^v24\\.22\\.2$' <<<\"$node_version\"\n"
                "        run: echo ok\n"
            )
            path.write_text(text, encoding="utf-8")
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(problems, "missing version assertion")

    def test_inline_run_node_assertion_valid(self) -> None:
        # Positive: an inline run value carries the assertion.
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-go.yml"
            text = path.read_text(encoding="utf-8")
            text = text.replace(
                'node_version="$(node --version)"; grep -Eq \'^v24\\.22\\.2$\' <<<"$node_version"',
                "echo ok",
            )
            text += (
                "      - name: Verify node\n"
                "        run: node_version=\"$(node --version)\"; grep -Eq '^v24\\.22\\.2$' <<<\"$node_version\"\n"
            )
            path.write_text(text, encoding="utf-8")
            self.assertEqual(check_release_tool_pins(root), [])

    def test_planted_commented_node_install_pin(self) -> None:
        # Tests detection when setup-node uses action line in release-go.yml is commented out.
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-go.yml"
            text = path.read_text(encoding="utf-8")
            text = text.replace(
                "uses: actions/setup-node",
                "# uses: actions/setup-node",
            )
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
        # Tests detection of unanchored node version assertion.
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-go.yml"
            text = path.read_text(encoding="utf-8")
            text = text.replace("'^v24\\.22\\.2$'", "'v24\\.22\\.2'")
            path.write_text(text, encoding="utf-8")
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(problems, "not anchored")

    def test_planted_node_assertion_trailing_or_true(self) -> None:
        # Tests detection of node assertion with trailing command bypass.
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-go.yml"
            text = path.read_text(encoding="utf-8")
            text = text.replace(
                'node_version="$(node --version)"; grep -Eq \'^v24\\.22\\.2$\' <<<"$node_version"',
                'node_version="$(node --version)"; grep -Eq \'^v24\\.22\\.2$\' <<<"$node_version" || true',
            )
            path.write_text(text, encoding="utf-8")
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(problems, "not anchored")

    def test_planted_pins_differ_across_files(self) -> None:
        # Tests detection of pins differing across workflow files.
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-build.yml"
            content = path.read_text(encoding="utf-8")
            content = content.replace("syft-version: v1.56.0", "syft-version: v1.55.0")
            content = content.replace("1\\.54\\.1", "1\\.54\\.0")
            path.write_text(content, encoding="utf-8")
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(problems, "syft: pin 1.56.0 != pin 1.55.0")

    def test_planted_env_differs(self) -> None:
        # Tests detection of tools/tool-versions.env differing from workflow pin.
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
                problems, "cosign: env 3.1.2 != workflow pin 3.1.4"
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
        # Tests detection of installer action lacking required version key.
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-build.yml"
            path.write_text(
                path.read_text(encoding="utf-8").replace(
                    "cosign-release: v3.1.4", "missing: v3.1.4"
                ),
                encoding="utf-8",
            )
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(
                problems, "sigstore/cosign-installer has no cosign-release pin"
            )

    def test_planted_assertion_without_version(self) -> None:
        # Tests detection of assertion regex without parseable version.
        with tempfile.TemporaryDirectory() as tmp_str:
            root = Path(tmp_str)
            create_valid_fixture(root)
            path = root / ".github/workflows/release-build.yml"
            path.write_text(
                path.read_text(encoding="utf-8").replace("3\\.1\\.4", "unparseable"),
                encoding="utf-8",
            )
            problems = check_release_tool_pins(root)
            self.assert_planted_defect(
                problems, "cosign: assertion regex yielded no version"
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


if __name__ == "__main__":
    unittest.main()
