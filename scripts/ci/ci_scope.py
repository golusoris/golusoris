#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Diff-aware CI scope calculation for pull requests and full matrices."""

from __future__ import annotations

import fnmatch
import os
import subprocess
import sys
from collections.abc import Sequence
from dataclasses import dataclass
from pathlib import Path

MAX_FILES = 100_000
MAX_MODULES = 64
MAX_REPLACES_PER_MODULE = 128
MAX_BFS_ITERATIONS = 4096
COMMAND_TIMEOUT_SECONDS = 120
# Exit 1 means "no match" for `any`; every error exits with this distinct code.
EXIT_ERROR = 3

GLOBAL_TRIGGERS: tuple[str, ...] = (
    "go.work*",
    ".golangci.yml",
    ".gosec.json",
    "tools/tool-versions.env",
    "scripts/ci/**",
    ".github/workflows/**",
    "Makefile",
    ".standards.yaml",
)


class ScopeError(RuntimeError):
    """Report a fail-closed scope resolution error."""


@dataclass(frozen=True)
class Scope:
    """Resolved CI scope."""

    mode: str  # "full" or "diff"
    reason: str
    base: str
    changed_files: tuple[str, ...]
    repo_root: Path


def find_repo_root(custom_root: Path | None = None) -> Path:
    """Resolve the git repository root directory."""
    if custom_root is not None:
        return custom_root.resolve(strict=True)
    try:
        proc = subprocess.run(
            ["git", "rev-parse", "--show-toplevel"],  # noqa: S607 - git executable on runner PATH.
            capture_output=True,
            text=True,
            check=True,
            timeout=COMMAND_TIMEOUT_SECONDS,
        )
        return Path(proc.stdout.strip()).resolve(strict=True)
    except (OSError, subprocess.SubprocessError) as err:
        raise ScopeError(f"cannot resolve repository root: {err}") from err


class GitError(RuntimeError):
    """Raised when a git invocation fails."""

    def __init__(self, message: str, returncode: int) -> None:
        super().__init__(message)
        self.returncode = returncode


def _run_git(args: Sequence[str], cwd: Path) -> bytes:
    """Execute a git command with timeout and fail-closed error handling."""
    proc = subprocess.run(  # noqa: S603 - fixed git command without shell.
        ["git", "-C", str(cwd), *args],  # noqa: S607 - git executable on runner PATH.
        capture_output=True,
        timeout=COMMAND_TIMEOUT_SECONDS,
        check=False,
    )
    if proc.returncode != 0:
        err_msg = proc.stderr.decode("utf-8", errors="replace").strip()
        sys.stderr.write(f"ci-scope: git error (exit {proc.returncode}): {err_msg}\n")
        raise GitError(err_msg, proc.returncode)
    return proc.stdout


def _matches_any_pattern(path: str, patterns: Sequence[str]) -> bool:
    """Check if normalized path matches any glob pattern."""
    for pattern in patterns[:MAX_FILES]:
        if fnmatch.fnmatch(path, pattern):
            return True
    return False


def resolve_scope(repo_root: Path | None = None) -> Scope:
    """Determine whether the run is diff-aware or full, printing to stderr."""
    root = find_repo_root(repo_root)
    base_sha = os.environ.get("CI_SCOPE_BASE", "").strip()
    if not base_sha:
        sys.stderr.write("ci-scope: full (CI_SCOPE_BASE unset)\n")
        return Scope(
            mode="full",
            reason="CI_SCOPE_BASE unset",
            base="",
            changed_files=(),
            repo_root=root,
        )

    # Base SHA is set: run git diff against HEAD (fail closed on invalid base)
    raw_output = _run_git(["diff", "--name-only", "-z", f"{base_sha}...HEAD"], cwd=root)
    # Also include uncommitted working tree changes (for local worktree testing)
    worktree_output = _run_git(["diff", "--name-only", "-z", "HEAD"], cwd=root)

    combined_raw = raw_output + b"\0" + worktree_output
    entries = []
    seen = set()
    for p in combined_raw.split(b"\0"):
        if not p:
            continue
        normalized = p.decode("utf-8", errors="replace").replace("\\", "/")
        if normalized not in seen:
            seen.add(normalized)
            entries.append(normalized)
    if len(entries) > MAX_FILES:
        raise ScopeError(f"diff file bound exceeded: {len(entries)} > {MAX_FILES}")

    changed_files = tuple(entries)

    # Check for global triggers
    for path in changed_files[:MAX_FILES]:
        for trigger in GLOBAL_TRIGGERS:
            if fnmatch.fnmatch(path, trigger):
                sys.stderr.write(f"ci-scope: full (global trigger: {path})\n")
                return Scope(
                    mode="full",
                    reason=f"global trigger: {path}",
                    base=base_sha,
                    changed_files=changed_files,
                    repo_root=root,
                )

    base12 = base_sha[:12]
    sys.stderr.write(
        f"ci-scope: diff-aware, {len(changed_files)} changed files vs {base12}\n"
    )
    return Scope(
        mode="diff",
        reason=f"diff vs {base12}",
        base=base_sha,
        changed_files=changed_files,
        repo_root=root,
    )


def discover_modules(repo_root: Path) -> tuple[str, ...]:
    """Discover all Go module directories in repo_root (root = '.')."""
    raw_output = _run_git(
        ["ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", "go.mod", "*/go.mod"],
        cwd=repo_root,
    )
    entries = [
        p.decode("utf-8", errors="replace").replace("\\", "/")
        for p in raw_output.split(b"\0")
        if p
    ]
    # Also support nested modules further down
    raw_output_all = _run_git(
        ["ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", "**/go.mod"],
        cwd=repo_root,
    )
    all_entries = [
        p.decode("utf-8", errors="replace").replace("\\", "/")
        for p in raw_output_all.split(b"\0")
        if p
    ]
    combined_modfiles = sorted(set(entries + all_entries))
    if len(combined_modfiles) > MAX_MODULES:
        raise ScopeError(f"discovered {len(combined_modfiles)} modules; max {MAX_MODULES}")

    modules: list[str] = []
    has_root = False
    for modfile in combined_modfiles:
        if modfile == "go.mod":
            has_root = True
            modules.append(".")
        elif modfile.endswith("/go.mod"):
            mod_dir = modfile[:-7]
            modules.append(mod_dir)

    if not has_root and (repo_root / "go.mod").is_file():
        modules.insert(0, ".")

    return tuple(sorted(modules, key=lambda m: (m != ".", m)))


def parse_module_replaces(repo_root: Path, known_modules: Sequence[str]) -> dict[str, list[str]]:
    """Build reverse dependency map: target_module -> list of modules that replace it."""
    dependents: dict[str, list[str]] = {m: [] for m in known_modules}
    known_set = set(known_modules)

    for mod in known_modules[:MAX_MODULES]:
        mod_dir = repo_root if mod == "." else repo_root / mod
        mod_file = mod_dir / "go.mod"
        if not mod_file.is_file():
            continue
        try:
            content = mod_file.read_text(encoding="utf-8", errors="replace")
        except OSError:
            continue

        replace_count = 0
        for line in content.splitlines():
            line = line.split("//")[0].strip()
            if "=>" not in line:
                continue
            replace_count += 1
            if replace_count > MAX_REPLACES_PER_MODULE:
                break
            parts = line.split("=>")
            target = parts[1].strip()
            # Local replace path must start with . or ..
            if target.startswith("./") or target.startswith("../") or target in (".", ".."):
                try:
                    resolved_target = (mod_dir / target).resolve()
                    dep_mod = os.path.relpath(resolved_target, repo_root).replace("\\", "/")
                except (ValueError, OSError):
                    continue
                if dep_mod in known_set and mod not in dependents.get(dep_mod, []):
                    dependents[dep_mod].append(mod)

    return dependents


def owner_module(file_path: str, sorted_modules: Sequence[str]) -> str:
    """Find the longest go.mod directory prefix for a changed file."""
    # sorted_modules should be ordered from longest prefix to shortest
    for mod in sorted_modules:
        if mod == ".":
            return "."
        if file_path == mod or file_path.startswith(mod + "/"):
            return mod
    return "."


def selected_modules(repo_root: Path | None = None) -> tuple[str, ...]:
    """Calculate the list of Go modules to check under the current scope."""
    root = find_repo_root(repo_root)
    scope = resolve_scope(root)
    all_mods = discover_modules(root)

    if scope.mode == "full":
        return all_mods

    if not scope.changed_files:
        return ()

    # Order modules by descending length of directory prefix for longest-prefix match
    descending_mods = sorted(all_mods, key=lambda m: (len(m) if m != "." else 0), reverse=True)
    dependents = parse_module_replaces(root, all_mods)

    selected: set[str] = set()
    for changed in scope.changed_files[:MAX_FILES]:
        owner = owner_module(changed, descending_mods)
        selected.add(owner)

    # Transitive expansion of dependents
    queue = list(selected)
    iterations = 0
    while queue and iterations < MAX_BFS_ITERATIONS:
        iterations += 1
        current = queue.pop(0)
        for dep in dependents.get(current, []):
            if dep not in selected:
                selected.add(dep)
                queue.append(dep)

    return tuple(sorted(selected, key=lambda m: (m != ".", m)))


def matching_files(globs: Sequence[str], repo_root: Path | None = None) -> tuple[str, ...]:
    """Return changed tracked files matching globs (diff) or all ls-files (full)."""
    root = find_repo_root(repo_root)
    scope = resolve_scope(root)

    if scope.mode == "full":
        raw_output = _run_git(
            ["ls-files", "-z", "--cached", "--others", "--exclude-standard"],
            cwd=root,
        )
        candidates = [
            p.decode("utf-8", errors="replace").replace("\\", "/")
            for p in raw_output.split(b"\0")
            if p
        ]
    else:
        candidates = [
            f for f in scope.changed_files
            if (root / f).is_file()
        ]

    matched = [
        f for f in candidates[:MAX_FILES]
        if _matches_any_pattern(f, globs)
    ]
    return tuple(sorted(set(matched)))


def matches_any(globs: Sequence[str], repo_root: Path | None = None) -> bool:
    """Return True if any changed file matches any glob (full mode always True)."""
    root = find_repo_root(repo_root)
    scope = resolve_scope(root)
    if scope.mode == "full":
        return True
    for changed in scope.changed_files[:MAX_FILES]:
        if _matches_any_pattern(changed, globs):
            return True
    return False


def _parse_cli_args(args: Sequence[str]) -> tuple[Path | None, list[str]]:
    """Extract optional --root argument and remaining subcommand arguments."""
    root: Path | None = None
    cleaned: list[str] = []
    index = 0
    while index < len(args):
        if args[index] == "--root" and index + 1 < len(args):
            root = Path(args[index + 1])
            index += 2
        else:
            cleaned.append(args[index])
            index += 1
    return root, cleaned


def _dispatch_command(command: str, sub_args: list[str], root: Path | None) -> int:
    """Dispatch CLI command to the appropriate handler."""
    if command == "modules":
        for mod in selected_modules(root):
            sys.stdout.write(f"{mod}\n")
        return 0

    if command == "files":
        if not sub_args:
            sys.stderr.write("usage: ci_scope.py files <glob>...\n")
            return 2
        for matched_file in matching_files(sub_args, root):
            sys.stdout.write(f"{matched_file}\n")
        return 0

    if command == "any":
        if not sub_args:
            sys.stderr.write("usage: ci_scope.py any <glob>...\n")
            return 2
        return 0 if matches_any(sub_args, root) else 1

    sys.stderr.write(f"unknown command: {command}\n")
    return 2


def main(argv: list[str] | None = None) -> int:
    """CLI entrypoint."""
    try:
        args = argv if argv is not None else sys.argv[1:]
        root, cleaned_args = _parse_cli_args(args)
        if not cleaned_args:
            sys.stderr.write(
                "usage: ci_scope.py [--root PATH] <modules|files <glob>...|any <glob>...>\n"
            )
            return 2

        return _dispatch_command(cleaned_args[0], cleaned_args[1:], root)
    except GitError:
        return EXIT_ERROR
    except (OSError, RuntimeError, ValueError, subprocess.SubprocessError) as err:
        sys.stderr.write(f"ci-scope: error: {err}\n")
        return EXIT_ERROR


if __name__ == "__main__":
    sys.exit(main())
