# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Enforce shell hygiene against SIGPIPE on early-exit readers and bare module downloads."""

from __future__ import annotations

import json
import re
import subprocess  # Runs only the fixed git ls-files command below. # nosec B404
import sys
from fnmatch import fnmatch
from pathlib import Path
from typing import NamedTuple

BARE_DOWNLOAD_RE = re.compile(
    r"(?:^|[;&|({]|(?:\$\()|\b(?:then|do|run:))\s*"
    r"((?:[A-Za-z_][A-Za-z0-9_]*=[^\s;&|()]+\s+)*)"
    r"(?:(\$\(GO\)\s+install)|(go\s+(?:install|get|mod\s+download))\b)"
)

SCOPE_PATTERNS = (
    ".github/**/*.yml",
    ".github/**/*.yaml",
    ".gitea/**/*.yml",
    ".gitea/**/*.yaml",
    "scripts/**/*.sh",
    "tools/*.sh",
    ".claude/hooks/*.sh",
)

EXCLUDED_EXACT = {
    ".config/lefthook/engine.sh",
    "tools/.pre-commit-config.yaml",
}

YAML_BLOCK_SCALAR_RE = re.compile(r"^\s*(?:[a-zA-Z0-9_-]+:\s*)?[|>][-+0-9]*\s*$")
MAX_SCAN_FILES = 20000
MAX_FILE_LINES = 10000


class Finding(NamedTuple):
    """Represent one policy finding."""

    path: str
    line: int
    rule: str
    message: str


def get_praetor_files(repo_root: Path) -> set[str]:
    """Read praetor-managed files from renovate.json packageRules."""
    renovate_path = repo_root / "renovate.json"
    if not renovate_path.is_file():
        return set()
    try:
        data = json.loads(renovate_path.read_text(encoding="utf-8"))
    except (json.JSONDecodeError, OSError):
        return set()
    praetor_paths: set[str] = set()
    for rule in data.get("packageRules", [])[:64]:
        if rule.get("description", "").startswith("praetor-managed files"):
            for name in rule.get("matchFileNames", [])[:256]:
                praetor_paths.add(str(name))
    return praetor_paths


def is_in_repo_scope(path_str: str, praetor_files: set[str]) -> bool:
    """Check if a file path belongs to the general lint scope."""
    if path_str in praetor_files or path_str in EXCLUDED_EXACT:
        return False
    normalized = path_str.replace("\\", "/")
    return any(fnmatch(normalized, pat) for pat in SCOPE_PATTERNS)


def is_in_download_scope(path_str: str, praetor_files: set[str]) -> bool:
    """Check if a file path belongs to the bare module download check."""
    normalized = path_str.replace("\\", "/")
    if normalized == "Makefile":
        return True
    if normalized.endswith(("_test.sh", "_test.py")) or "-test.py" in normalized:
        return False
    if normalized == "scripts/ci/retry.sh":
        return False
    return is_in_repo_scope(normalized, praetor_files)


class QuoteState:
    """Track shell quote and context stack across characters."""

    def __init__(self, max_depth: int = 10000) -> None:
        self.stack: list[str] = ["code"]
        self.escaped = False
        self.saw_dollar = False
        self.word_start = True
        self.max_depth = max_depth

    def is_active(self) -> bool:
        """Check whether current position is outside quotes and unescaped."""
        return self.stack[-1] in ("code", "cmd", "paren") and not self.escaped

    def _push(self, context: str) -> None:
        """Push a new context onto stack if within bounds."""
        if len(self.stack) < self.max_depth:
            self.stack.append(context)

    def _step_code(self, char: str) -> None:
        """Handle unquoted shell code, subshell parens, and command substitution."""
        if self.escaped:
            self.escaped = False
            return
        if char == "\\":
            self.escaped = True
            self.saw_dollar = False
            return
        if self.saw_dollar:
            self.saw_dollar = False
            if char == "(":
                self._push("cmd")
                return
            if char == "'":
                self._push("ansi")
                return
        self._step_code_tokens(char)

    def _step_code_tokens(self, char: str) -> None:
        """Handle syntax tokens in code context outside escapes and dollar sequences."""
        if char == "'":
            self._push("single")
        elif char == '"':
            self._push("double")
        elif char == "$":
            self.saw_dollar = True
        elif char == "(":
            self._push("paren")
        elif char == ")" and self.stack[-1] in ("cmd", "paren"):
            self.stack.pop()

    def _step_double(self, char: str) -> None:
        """Handle double-quote context tracking escapes, closing quote, and command substitution."""
        if self.escaped:
            self.escaped = False
            return
        if char == "\\":
            self.escaped = True
            self.saw_dollar = False
            return
        if self.saw_dollar:
            self.saw_dollar = False
            if char == "(":
                self._push("cmd")
                return
        if char == '"':
            self.stack.pop()
        elif char == "$":
            self.saw_dollar = True

    def _step_single(self, char: str) -> None:
        """Handle single-quote context where only unescaped quote exits."""
        if char == "'":
            self.stack.pop()

    def _step_ansi(self, char: str) -> None:
        """Handle ANSI-C quote context where backslash escapes next char."""
        if self.escaped:
            self.escaped = False
            return
        if char == "\\":
            self.escaped = True
        elif char == "'":
            self.stack.pop()

    def step(self, char: str) -> None:
        """Advance scanner state by one character."""
        was_escaped = self.escaped
        top = self.stack[-1]
        if top in ("code", "cmd", "paren"):
            self._step_code(char)
        elif top == "double":
            self._step_double(char)
        elif top == "single":
            self._step_single(char)
        elif top == "ansi":
            self._step_ansi(char)
        self.word_start = char.isspace() and not was_escaped and top in ("code", "cmd", "paren")


def strip_comment(line: str) -> str:
    """Strip shell comment outside quotes."""
    out: list[str] = []
    state = QuoteState(max_depth=max(1, len(line)))
    for char in line:
        if state.is_active() and char == "#" and state.word_start:
            break
        state.step(char)
        out.append(char)
    return "".join(out).rstrip()


def split_pipeline_stages(line: str) -> list[str]:
    """Split line on unquoted pipe operators outside logical OR."""
    stages: list[str] = []
    curr: list[str] = []
    state = QuoteState(max_depth=max(1, len(line)))
    idx = 0
    total = len(line)
    while idx < total:
        char = line[idx]
        if state.is_active() and char == "|":
            if idx + 1 < total and line[idx + 1] == "|":
                curr.append("||")
                state.step("|")
                state.step("|")
                idx += 2
                continue
            stages.append("".join(curr))
            curr = []
            state.step("|")
            if idx + 1 < total and line[idx + 1] == "&":
                state.step("&")
                idx += 2
            else:
                idx += 1
            continue
        state.step(char)
        curr.append(char)
        idx += 1
    stages.append("".join(curr))
    return stages


def has_early_exit_grep_flag(rest: str) -> bool:
    """Check whether grep arguments contain early-exit options."""
    if re.search(r">\s*/dev/null", rest):
        return True
    long_flags = ("--quiet", "--silent", "--files-with-matches")
    for tok in rest.split():
        if tok in long_flags or tok.startswith("--max-count"):
            return True
        if tok.startswith("-") and not tok.startswith("--"):
            if any(c in "qmlL" for c in tok.lstrip("-")):
                return True
        elif not tok.startswith("-"):
            break
    return False


def is_early_exit_reader(stage: str) -> bool:
    """Detect if a pipeline stage is an early-exit reader."""
    cleaned = stage.strip()
    match = re.match(
        r"^(?:if\s+|while\s+|until\s+|!\s+|\{\s*|\(\s*)*(?:[A-Za-z_][A-Za-z0-9_]*=[^\s;&|()]+\s+)*([a-zA-Z0-9_.-]+)\b(.*)$",
        cleaned,
        re.DOTALL,
    )
    if not match:
        return False
    cmd, rest = match.group(1), match.group(2)
    if cmd == "head":
        return True
    if cmd == "grep":
        return has_early_exit_grep_flag(rest)
    return False


def join_continuations(raw_lines: list[str]) -> list[tuple[int, str]]:
    """Join line continuations ending in backslash or pipe."""
    joined: list[tuple[int, str]] = []
    idx = 0
    total = min(len(raw_lines), MAX_FILE_LINES)
    while idx < total:
        start_line = idx + 1
        current = raw_lines[idx].rstrip("\r\n")
        idx += 1
        while idx < total:
            stripped = strip_comment(current).rstrip()
            if YAML_BLOCK_SCALAR_RE.match(stripped):
                break
            if stripped.endswith("\\") or stripped.endswith(("|", "|&")):
                if stripped.endswith("\\"):
                    stripped = stripped[:-1].rstrip()
                next_part = raw_lines[idx].rstrip("\r\n").lstrip()
                current = f"{stripped} {next_part}"
                idx += 1
            else:
                break
        joined.append((start_line, current))
    return joined


def check_early_exit_reader(path: str, raw_lines: list[str]) -> list[Finding]:
    """Flag pipes whose reader can exit early causing SIGPIPE."""
    findings: list[Finding] = []
    for lno, text in join_continuations(raw_lines):
        stripped = strip_comment(text)
        stages = split_pipeline_stages(stripped)
        if len(stages) <= 1:
            continue
        for stage in stages[1:]:
            if is_early_exit_reader(stage):
                findings.append(
                    Finding(
                        path,
                        lno,
                        "early-exit-reader",
                        f"pipe reader can exit before reading all input: {stage.strip()}",
                    )
                )
                break
    return findings


def check_bare_module_download(path: str, raw_lines: list[str]) -> list[Finding]:
    """Flag bare go install/get/mod download commands missing retry wrapper."""
    findings: list[Finding] = []
    for lno, line in enumerate(raw_lines[:MAX_FILE_LINES], 1):
        stripped = strip_comment(line)
        match = BARE_DOWNLOAD_RE.search(stripped)
        if not match:
            continue
        cmd_text = match.group(0).strip()
        findings.append(
            Finding(
                path,
                lno,
                "bare-module-download",
                f"bare module download not wrapped with retry.sh: {cmd_text}",
            )
        )
    return findings


def scan_file(path_str: str, check_download: bool) -> list[Finding]:
    """Run applicable rules on one file path."""
    target = Path(path_str)
    if not target.is_file():
        return []
    try:
        raw_lines = target.read_text(encoding="utf-8", errors="ignore").splitlines()
    except OSError:
        return []
    findings: list[Finding] = []
    if len(raw_lines) > MAX_FILE_LINES:
        findings.append(
            Finding(
                path_str,
                MAX_FILE_LINES,
                "scan-limit",
                f"file line count exceeds scan limit {MAX_FILE_LINES}: {len(raw_lines)}",
            )
        )
    findings.extend(check_early_exit_reader(path_str, raw_lines))
    if check_download:
        findings.extend(check_bare_module_download(path_str, raw_lines))
    return findings


def collect_repo_files(repo_root: Path) -> tuple[list[str], list[Finding]]:
    """Collect tracked files via git ls-files."""
    try:
        res = subprocess.check_output(  # noqa: S603 - fixed git command without shell. # nosec B603
            ["git", "-C", str(repo_root), "ls-files"],  # noqa: S607 - git executable on runner PATH. # nosec B607
            text=True,
            timeout=60,
        )
    except (subprocess.SubprocessError, OSError) as err:
        return [], [Finding(".", 0, "scan-limit", f"git ls-files failed: {err}")]
    all_files = res.splitlines()
    findings: list[Finding] = []
    if len(all_files) > MAX_SCAN_FILES:
        findings.append(
            Finding(
                ".",
                0,
                "scan-limit",
                f"tracked file count exceeds scan limit {MAX_SCAN_FILES}: {len(all_files)}",
            )
        )
    return all_files[:MAX_SCAN_FILES], findings


def scan_arguments(args: list[str]) -> list[Finding]:
    """Scan explicitly provided file paths."""
    findings: list[Finding] = []
    for arg in args:
        norm = arg.replace("\\", "/")
        dl_scope = (
            not norm.endswith(("_test.sh", "_test.py"))
            and "-test.py" not in norm
            and norm != "scripts/ci/retry.sh"
        )
        findings.extend(scan_file(arg, dl_scope))
    return findings


def scan_repository(repo_root: Path, praetor_files: set[str]) -> list[Finding]:
    """Scan all in-scope repository files."""
    files, findings = collect_repo_files(repo_root)
    for rel in files:
        reader_ok = is_in_repo_scope(rel, praetor_files)
        dl_ok = is_in_download_scope(rel, praetor_files)
        if reader_ok or dl_ok:
            findings.extend(scan_file(rel, dl_ok))
    return findings


def main() -> int:
    """Run flake policy checks on specified paths or repository files."""
    repo_root = Path(__file__).resolve().parent.parent.parent
    if len(sys.argv) > 1:
        findings = scan_arguments(sys.argv[1:])
    else:
        findings = scan_repository(repo_root, get_praetor_files(repo_root))

    findings.sort(key=lambda f: (f.path, f.line))
    for f in findings:
        print(f"{f.path}:{f.line}: {f.rule}: {f.message}")

    return 1 if findings else 0


if __name__ == "__main__":
    sys.exit(main())
