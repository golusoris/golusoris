#!/usr/bin/env python3
# Ported from plurano/amussis tools/regenerate.py (commit 2c0e2aa).
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
"""Regenerate generated repository files and write an artifact manifest.

When dependencies are bumped on renovate branches, modules that replace a bumped
local module must be tidied so go.mod and go.sum match the updated graph, and
embedded assets such as the Scalar API reference browser bundle are rebuilt.
This tool executes go mod tidy across all modules, rebuilds the Scalar bundle,
verifies that no unexpected files were modified or deleted, and writes a directory
containing the regenerated files and a manifest describing them.

    write       run update commands, verify changes, and write artifact to --out
    check       hold the table below to the declared list .github/generated-files
    selftest    verify plant refusals and accepted cases on a fixture repository

The merge train of plurano/horistes commits the artifact to the dependency
update; it reads .github/generated-files from the default branch and commits
no path missing there, so that file and GENERATED must name the same paths.

Exit status: 0 success or nothing to regenerate, 1 unexpected changes or
generated file deleted, 2 the tool could not run or command failed.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile
from collections.abc import Callable
from pathlib import Path
from typing import Any

SCHEMA = "horistes.generated-files.v1"
DECLARED = ".github/generated-files"
MODULE_TIMEOUT_SECONDS = 300
DEFAULT_TIMEOUT_SECONDS = 60
SCALAR_TIMEOUT_SECONDS = 600
GIT_TIMEOUT_SECONDS = 60
MODULES_DISCOVERY_TIMEOUT_SECONDS = 60
MAX_STATUS_ENTRIES = 2048
MAX_MODULES = 64
MAX_CHANGED_FILES = 256
MAX_DECLARED_LINES = 1024
# A replace chain settles one link per pass; the deepest chain here is four modules.
MAX_TIDY_PASSES = 6
SCALAR_PATHS = [
    "apidocs/embed/SCALAR_RELEASED",
    "apidocs/embed/scalar.js",
    "docs/upstream/README.md",
    "docs/upstream/scalar/README.md",
]


class CheckError(Exception):
    """Raised when regeneration or verification cannot proceed."""


def declared_paths(text: str) -> list[str]:
    """The paths of a .github/generated-files text: no comments, no blank lines."""
    lines = text.splitlines()
    bound = min(len(lines), MAX_DECLARED_LINES)
    out: list[str] = []
    for i in range(bound):
        line = lines[i].strip()
        if line and not line.startswith("#"):
            out.append(line)
    return out


def discover_modules(
    root: Path, timeout: int = MODULES_DISCOVERY_TIMEOUT_SECONDS
) -> list[str]:
    script = root / "scripts" / "ci" / "go-modules.sh"
    if not script.is_file():
        raise CheckError(f"module discovery script not found: {script}")
    try:
        proc = subprocess.run(  # noqa: S603 - fixed bash command on a repository script.
            ["bash", str(script), "list"],  # noqa: S607 - bash executable on runner PATH.
            cwd=root,
            capture_output=True,
            text=True,
            timeout=timeout,
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        raise CheckError(f"go-modules.sh list failed to run: {error}") from error
    if proc.returncode != 0:
        err = proc.stderr.strip()
        raise CheckError(f"go-modules.sh list exited {proc.returncode}: {err}")
    lines = [line.strip() for line in proc.stdout.splitlines() if line.strip()]
    if not lines or len(lines) > MAX_MODULES:
        raise CheckError(f"unexpected module count: {len(lines)}")
    return lines


def get_generated_files(root: Path) -> list[str]:
    modules = discover_modules(root)
    paths: list[str] = []
    bound = min(len(modules), MAX_MODULES)
    for i in range(bound):
        m = modules[i]
        if m == ".":
            paths.extend(["go.mod", "go.sum"])
        else:
            paths.extend([f"{m}/go.mod", f"{m}/go.sum"])
    paths.extend(SCALAR_PATHS)
    return sorted(paths)


def check_declared(root: Path, expected: list[str] | None = None) -> list[str]:
    try:
        text = (root / DECLARED).read_text(encoding="utf-8")
    except OSError as error:
        raise CheckError(f"{DECLARED} could not be read: {error}") from error
    listed = declared_paths(text)
    if expected is None:
        expected = get_generated_files(root)
    if sorted(listed) != sorted(expected) or len(listed) != len(set(listed)):
        diffs: list[str] = []
        missing = sorted(set(expected) - set(listed))
        extra = sorted(set(listed) - set(expected))
        if missing:
            diffs.append(f"missing: {missing}")
        if extra:
            diffs.append(f"unexpected: {extra}")
        if len(listed) != len(set(listed)):
            dups = sorted({x for x in listed if listed.count(x) > 1})
            diffs.append(f"duplicate entries: {dups}")
        return [f"{DECLARED} differs from generated table ({'; '.join(diffs)})"]
    return []


def run_git(args: list[str], root: Path, timeout: int = GIT_TIMEOUT_SECONDS) -> str:
    try:
        proc = subprocess.run(  # noqa: S603 - git with arguments built in this file.
            ["git", *args],  # noqa: S607 - git executable on runner PATH.
            cwd=root,
            capture_output=True,
            text=True,
            timeout=timeout,
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        raise CheckError(f"git {' '.join(args)} could not run: {error}") from error
    if proc.returncode != 0:
        err = proc.stderr.strip()
        raise CheckError(f"git {' '.join(args)} exited {proc.returncode}: {err}")
    return proc.stdout


def git_status_bytes(root: Path, timeout: int = GIT_TIMEOUT_SECONDS) -> bytes:
    try:
        proc = subprocess.run(
            ["git", "status", "--porcelain=v1", "-z"],  # noqa: S607 - git executable on runner PATH.
            cwd=root,
            capture_output=True,
            timeout=timeout,
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        raise CheckError(f"git status could not run: {error}") from error
    if proc.returncode != 0:
        err = proc.stderr.decode("utf-8", errors="replace")
        raise CheckError(f"git status exited {proc.returncode}: {err}")
    return proc.stdout


def parse_status_entries(raw: bytes) -> list[tuple[str, str, str | None]]:
    if not raw:
        return []
    tokens = raw.split(b"\0")
    if tokens and tokens[-1] == b"":
        tokens.pop()
    entries: list[tuple[str, str, str | None]] = []
    idx = 0
    bound = min(len(tokens), MAX_STATUS_ENTRIES)
    for _ in range(bound):
        if idx >= len(tokens):
            break
        item = tokens[idx]
        idx += 1
        if len(item) < 3:
            continue
        code = item[:2].decode("ascii", errors="replace")
        path = item[3:].decode("utf-8", errors="replace")
        orig_path = None
        if ("R" in code or "C" in code) and idx < len(tokens):
            orig_path = tokens[idx].decode("utf-8", errors="replace")
            idx += 1
        entries.append((code, path, orig_path))
    return entries


def evaluate_changes(
    entries: list[tuple[str, str, str | None]],
    root: Path,
    allowed: set[str],
    pre_existing: set[str],
) -> tuple[list[str], list[str]]:
    failures: list[str] = []
    changed: list[str] = []
    bound = min(len(entries), MAX_STATUS_ENTRIES)
    for i in range(bound):
        code, path, orig = entries[i]
        if path not in allowed:
            failures.append(f"unexpected changed path: {path}")
        if orig is not None and orig not in allowed:
            failures.append(f"unexpected changed path: {orig}")
        is_deleted = "D" in code or not (root / path).is_file()
        if is_deleted:
            if path in allowed and path in pre_existing:
                failures.append(f"generated path was deleted: {path}")
        elif path in allowed:
            changed.append(path)
    return failures, sorted(set(changed))


def run_update_command(
    cmd: list[str],
    cwd: Path,
    timeout: int,
    env: dict[str, str] | None = None,
) -> None:
    try:
        proc = subprocess.run(  # noqa: S603 - go command lines built in this file.
            cmd,
            cwd=cwd,
            env=env,
            capture_output=True,
            text=True,
            timeout=timeout,
            check=False,
        )
    except subprocess.TimeoutExpired as error:
        raise CheckError(f"{' '.join(cmd)} timed out after {timeout} s") from error
    except OSError as error:
        raise CheckError(f"{' '.join(cmd)} could not run: {error}") from error
    if proc.returncode != 0:
        err = proc.stderr.strip() or proc.stdout.strip()
        raise CheckError(f"{' '.join(cmd)} exited {proc.returncode}:\n{err}")


def format_command(cmd: list[str]) -> list[str]:
    return ["python3" if arg == sys.executable else arg for arg in cmd]


def build_manifest(
    head: str, files: list[dict[str, Any]], commands: list[list[str]]
) -> str:
    manifest = {
        "commands": [format_command(cmd) for cmd in commands],
        "files": sorted(files, key=lambda item: item["path"]),
        "head": head,
        "schema": SCHEMA,
    }
    return json.dumps(manifest, indent=2, sort_keys=True) + "\n"


def snapshot(root: Path, paths: set[str]) -> dict[str, bytes]:
    return {p: (root / p).read_bytes() for p in sorted(paths) if (root / p).is_file()}


def tidy_until_stable(
    root: Path,
    modules: list[str],
    allowed: set[str],
    after_pass: Callable[[Path], None] | None = None,
) -> list[list[str]]:
    """Tidy every module, pass after pass, until a pass changes no generated file."""
    env = os.environ.copy()
    env["GOFLAGS"] = "-mod=mod"
    cmds = [["go", "-C", m, "mod", "tidy"] for m in modules[:MAX_MODULES]]
    before = snapshot(root, allowed)
    for _ in range(MAX_TIDY_PASSES):
        for cmd in cmds:
            run_update_command(cmd, root, MODULE_TIMEOUT_SECONDS, env)
        if after_pass is not None:
            after_pass(root)
        after = snapshot(root, allowed)
        if after == before:
            return cmds
        before = after
    raise CheckError(f"go mod tidy did not converge after {MAX_TIDY_PASSES} passes")


def copy_changed_files(
    root: Path, out: Path, changed: list[str]
) -> list[dict[str, Any]]:
    records: list[dict[str, Any]] = []
    bound = min(len(changed), MAX_CHANGED_FILES)
    for i in range(bound):
        path_str = changed[i]
        src = root / path_str
        dst = out / "files" / path_str
        dst.parent.mkdir(parents=True, exist_ok=True)
        data = src.read_bytes()
        dst.write_bytes(data)
        records.append(
            {
                "bytes": len(data),
                "path": path_str,
                "sha256": hashlib.sha256(data).hexdigest(),
            }
        )
    return records


def write_regenerated(
    out: Path,
    root: Path,
    commands: dict[str, list[str]] | None = None,
    after_pass: Callable[[Path], None] | None = None,
) -> tuple[list[str], str | None]:
    head = run_git(["rev-parse", "HEAD"], root).strip()
    if len(head) != 40 or not all(c in "0123456789abcdefABCDEF" for c in head):
        raise CheckError(f"git rev-parse HEAD returned invalid commit: {head!r}")
    if git_status_bytes(root):
        raise CheckError("working tree is dirty before regeneration")

    executed: list[list[str]] = []
    if commands is not None:
        allowed = set(commands.keys())
        pre_existing = {p for p in allowed if (root / p).is_file()}
        for cmd in commands.values():
            run_update_command(cmd, root, DEFAULT_TIMEOUT_SECONDS)
            executed.append(cmd)
    else:
        allowed = set(get_generated_files(root))
        pre_existing = {p for p in allowed if (root / p).is_file()}
        executed = tidy_until_stable(root, discover_modules(root), allowed, after_pass)
        scalar_script = root / "scripts" / "ci" / "scalar-bundle-update.sh"
        if scalar_script.is_file():
            scalar_cmd = ["bash", "scripts/ci/scalar-bundle-update.sh", "regenerate"]
            run_update_command(scalar_cmd, root, SCALAR_TIMEOUT_SECONDS)
            executed.append(scalar_cmd)

    raw_status = git_status_bytes(root)
    if not raw_status:
        return [], "nothing to regenerate"
    entries = parse_status_entries(raw_status)
    failures, changed = evaluate_changes(entries, root, allowed, pre_existing)
    if failures:
        return failures, None
    if not changed:
        return [], "nothing to regenerate"

    file_records = copy_changed_files(root, out, changed)
    manifest_text = build_manifest(head, file_records, executed)
    out.mkdir(parents=True, exist_ok=True)
    (out / "manifest.json").write_text(manifest_text, encoding="utf-8")
    return [], f"{len(file_records)} files at {head}"


FIXTURE_GENERATED = sorted(
    [
        "a/go.mod",
        "a/go.sum",
        "b/go.mod",
        "b/go.sum",
        "c/go.mod",
        "c/go.sum",
        "go.mod",
        "go.sum",
        *SCALAR_PATHS,
    ]
)

# c has no dependencies; a needs c and b needs a, each joined by a local replace.
FIXTURE_FILES = {
    "go.mod": "module example.com/fixture\n\ngo 1.24\n",
    "fixture.go": "package fixture\n",
    "c/go.mod": "module example.com/c\n\ngo 1.24\n",
    "c/c.go": "package c\n\nconst V = 1\n",
    "a/go.mod": (
        "module example.com/a\n\ngo 1.24\n\n"
        "require example.com/c v0.0.0\nreplace example.com/c => ../c\n"
    ),
    "a/a.go": 'package a\n\nimport "example.com/c"\n\nconst A = c.V\n',
    "b/go.mod": (
        "module example.com/b\n\ngo 1.24\n\n"
        "require (\n\texample.com/a v0.0.0\n\texample.com/c v0.0.0\n)\n"
        "replace (\n\texample.com/a => ../a\n\texample.com/c => ../c\n)\n"
    ),
    "b/b.go": 'package b\n\nimport "example.com/a"\n\nconst B = a.A\n',
}

# Renovate's shape: a's requirement moves and a is tidied, b is left as it was.
STALE_A_GO_MOD = (
    "module example.com/a\n\ngo 1.24\n\n"
    "require example.com/c v0.1.0\nreplace example.com/c => ../c\n"
)

STALE_REFUSAL = "/b: go mod download rewrote go.mod or go.sum"


def offline_env() -> dict[str, str]:
    return {**os.environ, "GOPROXY": "off", "GOFLAGS": "-mod=mod"}


def fixture_tidy(module_dir: Path) -> None:
    subprocess.run(
        ["go", "mod", "tidy"],  # noqa: S607 - go executable on runner PATH.
        cwd=module_dir,
        env=offline_env(),
        check=True,
        timeout=DEFAULT_TIMEOUT_SECONDS,
    )


def fixture_verify(repo: Path) -> subprocess.CompletedProcess[str]:
    """The Build job's verify step, on the fixture's own copy of go-modules.sh."""
    return subprocess.run(
        ["bash", "scripts/ci/go-modules.sh", "verify"],  # noqa: S607 - bash executable on runner PATH.
        cwd=repo,
        env=offline_env(),
        capture_output=True,
        text=True,
        timeout=DEFAULT_TIMEOUT_SECONDS,
        check=False,
    )


def setup_fixture_repo(repo: Path, source_root: Path) -> str:
    run_git(["init"], repo)
    run_git(["config", "user.name", "Golusoris Tester"], repo)
    run_git(["config", "user.email", "test@example.com"], repo)
    (repo / ".gitignore").write_text("target/\n", encoding="utf-8")
    (repo / "tracked.txt").write_text("tracked fixture\n", encoding="utf-8")
    script = repo / "scripts" / "ci" / "go-modules.sh"
    script.parent.mkdir(parents=True)
    shutil.copy2(source_root / "scripts" / "ci" / "go-modules.sh", script)
    for rel, text in FIXTURE_FILES.items():
        (repo / rel).parent.mkdir(parents=True, exist_ok=True)
        (repo / rel).write_text(text, encoding="utf-8")
    for rel in (".", "c", "a", "b"):
        fixture_tidy(repo / rel)
    (repo / ".github").mkdir()
    (repo / DECLARED).write_text("\n".join(FIXTURE_GENERATED) + "\n", encoding="utf-8")
    run_git(["add", "."], repo)
    run_git(["commit", "-m", "initial fixture"], repo)
    return run_git(["rev-parse", "HEAD"], repo).strip()


def reset_fixture_repo(repo: Path) -> None:
    run_git(["reset", "--hard", "HEAD"], repo)
    run_git(["clean", "-fdx"], repo)


def plant_stale_consumer(repo: Path) -> str:
    reset_fixture_repo(repo)
    (repo / "a" / "go.mod").write_text(STALE_A_GO_MOD, encoding="utf-8")
    fixture_tidy(repo / "a")
    run_git(["commit", "-am", "bump a require c v0.1.0"], repo)
    return run_git(["rev-parse", "HEAD"], repo).strip()


def manifest_failures(out: Path, head: str) -> list[str]:
    manifest_file = out / "manifest.json"
    if not manifest_file.is_file():
        return ["case write-repairs manifest.json missing"]
    text = manifest_file.read_text(encoding="utf-8")
    data = json.loads(text)
    failures: list[str] = []
    if data.get("schema") != SCHEMA:
        failures.append(f"case write-repairs bad schema: {data.get('schema')}")
    if data.get("head") != head:
        failures.append(f"case write-repairs bad head: {data.get('head')} != {head}")
    entries = data.get("files", [])
    paths = [entry["path"] for entry in entries]
    if "b/go.mod" not in paths:
        failures.append(f"case write-repairs expected b/go.mod in files, got: {paths}")
    for entry in entries:
        copied = out / "files" / entry["path"]
        data_bytes = copied.read_bytes() if copied.is_file() else None
        if data_bytes is None:
            failures.append(f"case write-repairs copied file missing: {copied}")
        elif hashlib.sha256(data_bytes).hexdigest() != entry["sha256"]:
            failures.append(f"case write-repairs sha256 mismatch for {entry['path']}")
        elif len(data_bytes) != entry["bytes"]:
            failures.append(f"case write-repairs bytes mismatch for {entry['path']}")
    if text != json.dumps(data, indent=2, sort_keys=True) + "\n":
        failures.append(
            "case write-repairs manifest JSON not formatted deterministically"
        )
    return failures


def run_repair_cases(repo: Path, temp_dir: Path) -> tuple[list[str], int, int]:
    """A stale consumer fails verify with the workflow off, and passes after write."""
    failures: list[str] = []
    refused = accepted = 0
    head = plant_stale_consumer(repo)
    proc = fixture_verify(repo)
    if proc.returncode == 0:
        failures.append(
            "case stale-refused: go-modules.sh verify unexpectedly exited 0"
        )
    elif STALE_REFUSAL not in proc.stderr:
        failures.append(f"case stale-refused: wrong refusal: {proc.stderr.strip()}")
    else:
        refused += 1
    run_git(["reset", "--hard", "HEAD"], repo)
    out = temp_dir / "out-repairs"
    fails, _msg = write_regenerated(out, repo)
    failures.extend(f"case write-repairs unexpectedly failed: {f}" for f in fails)
    failures.extend(manifest_failures(out, head))
    proc = fixture_verify(repo)
    if proc.returncode != 0:
        failures.append(
            f"case write-repairs: verify failed after tidy: {proc.stderr.strip()}"
        )
    else:
        accepted += 1
    return failures, refused, accepted


def run_plant_cases_ab(repo: Path, temp_dir: Path) -> tuple[list[str], int]:
    failures: list[str] = []
    refused = 0

    # (a) command edits a non-generated tracked file
    reset_fixture_repo(repo)
    out_a = temp_dir / "out-a"
    cmd_a = {
        "b/go.mod": [
            sys.executable,
            "-c",
            "import pathlib; pathlib.Path('tracked.txt').write_text('dirty\\n')",
        ]
    }
    fails, _ = write_regenerated(out_a, repo, cmd_a)
    if not fails or not any("tracked.txt" in f for f in fails):
        failures.append(f"plant (a) not refused with tracked.txt: {fails}")
    else:
        refused += 1
    if (out_a / "manifest.json").exists():
        failures.append("plant (a) wrote manifest.json")

    # (b) command creates an untracked non-generated file
    reset_fixture_repo(repo)
    out_b = temp_dir / "out-b"
    cmd_b = {
        "b/go.mod": [
            sys.executable,
            "-c",
            "import pathlib; pathlib.Path('untracked.txt').write_text('new\\n')",
        ]
    }
    fails, _ = write_regenerated(out_b, repo, cmd_b)
    if not fails or not any("untracked.txt" in f for f in fails):
        failures.append(f"plant (b) not refused with untracked.txt: {fails}")
    else:
        refused += 1
    if (out_b / "manifest.json").exists():
        failures.append("plant (b) wrote manifest.json")

    return failures, refused


def run_plant_case_scalar_unlisted(repo: Path, temp_dir: Path) -> tuple[list[str], int]:
    """Proves an unlisted path is refused when the scalar step writes outside the table."""
    failures: list[str] = []
    refused = 0

    reset_fixture_repo(repo)
    out_scalar = temp_dir / "out-scalar-unlisted"
    script = repo / "scripts" / "ci" / "scalar-bundle-update.sh"
    script.parent.mkdir(parents=True, exist_ok=True)
    script.write_text(
        "#!/usr/bin/env bash\nprintf 'leak\\n' > outside_scalar.txt\n",
        encoding="utf-8",
    )
    script.chmod(0o755)
    run_git(["add", "scripts/ci/scalar-bundle-update.sh"], repo)
    run_git(["commit", "-m", "plant scalar script"], repo)

    fails, _ = write_regenerated(out_scalar, repo)
    if not fails or not any("outside_scalar.txt" in f for f in fails):
        failures.append(
            f"scalar unlisted plant not refused with outside_scalar.txt: {fails}"
        )
    else:
        refused += 1
    if (out_scalar / "manifest.json").exists():
        failures.append("scalar unlisted plant wrote manifest.json")

    run_git(["reset", "--hard", "HEAD~1"], repo)
    reset_fixture_repo(repo)
    return failures, refused


def run_plant_case_c(repo: Path, temp_dir: Path) -> tuple[list[str], int]:
    failures: list[str] = []
    refused = 0

    # (c) command deletes a generated file
    reset_fixture_repo(repo)
    out_c = temp_dir / "out-c"
    cmd_c = {
        "b/go.mod": [
            sys.executable,
            "-c",
            "import pathlib; pathlib.Path('b/go.mod').unlink()",
        ]
    }
    fails, _ = write_regenerated(out_c, repo, cmd_c)
    if not fails or not any("b/go.mod" in f for f in fails):
        failures.append(f"plant (c) not refused with deleted generated file: {fails}")
    else:
        refused += 1
    if (out_c / "manifest.json").exists():
        failures.append("plant (c) wrote manifest.json")

    return failures, refused


def run_plant_cases_de(repo: Path, temp_dir: Path) -> tuple[list[str], int]:
    failures: list[str] = []
    refused = 0

    # (d) command exits 1
    reset_fixture_repo(repo)
    out_d = temp_dir / "out-d"
    cmd_d = {"b/go.mod": [sys.executable, "-c", "import sys; sys.exit(1)"]}
    refused_d = False
    try:
        write_regenerated(out_d, repo, cmd_d)
    except CheckError as error:
        if "sys.exit(1)" in str(error):
            refused_d = True
        else:
            failures.append(f"plant (d) error did not name command: {error}")
    if not refused_d:
        failures.append("plant (d) command exit 1 was not refused")
    else:
        refused += 1
    if (out_d / "manifest.json").exists():
        failures.append("plant (d) wrote manifest.json")

    # (e) tree dirty before run
    reset_fixture_repo(repo)
    (repo / "dirty_pre.txt").write_text("pre-existing dirty file\n", encoding="utf-8")
    out_e = temp_dir / "out-e"
    cmd_e = {"b/go.mod": [sys.executable, "-c", ""]}
    refused_e = False
    try:
        write_regenerated(out_e, repo, cmd_e)
    except CheckError as error:
        if "dirty" in str(error):
            refused_e = True
        else:
            failures.append(f"plant (e) error did not mention dirty: {error}")
    if not refused_e:
        failures.append("plant (e) dirty tree was not refused")
    else:
        refused += 1
    if (out_e / "manifest.json").exists():
        failures.append("plant (e) wrote manifest.json")

    return failures, refused


def append_comment(root: Path) -> None:
    """A planted step that changes b/go.mod on every pass, so tidy never settles."""
    with (root / "b" / "go.mod").open("a", encoding="utf-8") as handle:
        handle.write("// pass\n")


def run_plant_case_f_convergence(repo: Path, temp_dir: Path) -> tuple[list[str], int]:
    failures: list[str] = []
    refused = 0

    # (f) non-converging update command
    reset_fixture_repo(repo)
    out_f = temp_dir / "out-f"
    refused_f = False
    try:
        write_regenerated(out_f, repo, after_pass=append_comment)
    except CheckError as error:
        if "converge" in str(error):
            refused_f = True
        else:
            failures.append(f"plant (f) error did not mention converge: {error}")
    if not refused_f:
        failures.append("plant (f) non-convergence was not refused")
    else:
        refused += 1
    if (out_f / "manifest.json").exists():
        failures.append("plant (f) wrote manifest.json")

    return failures, refused


def run_accepted_cases_gh(repo: Path, temp_dir: Path) -> tuple[list[str], int]:
    failures: list[str] = []
    accepted = 0

    # (g) command writes nothing
    reset_fixture_repo(repo)
    out_g = temp_dir / "out-g"
    cmd_g = {"b/go.mod": [sys.executable, "-c", ""]}
    fails, msg = write_regenerated(out_g, repo, cmd_g)
    if fails:
        failures.append(f"case (g) unexpectedly failed: {fails}")
    elif msg != "nothing to regenerate":
        failures.append(f"case (g) expected 'nothing to regenerate', got {msg!r}")
    else:
        accepted += 1
    if (out_g / "manifest.json").exists() or (out_g / "files").exists():
        failures.append("case (g) wrote output to DIR")

    # (h) command writes under ignored target/
    reset_fixture_repo(repo)
    out_h = temp_dir / "out-h"
    cmd_h = {
        "b/go.mod": [
            sys.executable,
            "-c",
            (
                "import pathlib; p = pathlib.Path('target/t.txt'); "
                "p.parent.mkdir(parents=True, exist_ok=True); p.write_text('build\\n')"
            ),
        ]
    }
    fails, msg = write_regenerated(out_h, repo, cmd_h)
    if fails:
        failures.append(f"case (h) unexpectedly failed: {fails}")
    elif msg != "nothing to regenerate":
        failures.append(f"case (h) expected 'nothing to regenerate', got {msg!r}")
    else:
        accepted += 1
    if (out_h / "manifest.json").exists() or (out_h / "files").exists():
        failures.append("case (h) wrote output to DIR")

    return failures, accepted


def run_declared_cases(repo: Path, generated: list[str]) -> tuple[list[str], int, int]:
    failures: list[str] = []
    refused = 0
    accepted = 0

    cases = (
        ("# comment\n\n" + "\n".join(generated) + "\n", False),
        ("\n".join(generated[:-1]) + "\n", True),
        ("\n".join(generated) + "\ntracked.txt\n", True),
        ("\n".join([*generated, generated[0]]) + "\n", True),
        ("\n".join(p for p in generated if p not in SCALAR_PATHS) + "\n", True),
    )
    for text, should_refuse in cases:
        (repo / ".github").mkdir(parents=True, exist_ok=True)
        (repo / DECLARED).write_text(text, encoding="utf-8")
        found = check_declared(repo, generated)
        if bool(found) != should_refuse:
            failures.append(
                f"declared list {text!r}: refused={bool(found)}, expected {should_refuse}"
            )
        elif should_refuse:
            refused += 1
        else:
            accepted += 1

    return failures, refused, accepted


def selftest(root: Path | None = None) -> tuple[list[str], int, int]:
    if root is None:
        root = Path(__file__).resolve().parents[2]
    with tempfile.TemporaryDirectory(
        prefix="golusoris-regen-selftest-"
    ) as temp_dir_str:
        temp_dir = Path(temp_dir_str)
        repo = temp_dir / "repo"
        repo.mkdir()
        setup_fixture_repo(repo, root)
        generated = list(FIXTURE_GENERATED)

        failures, refused_rep, accepted_rep = run_repair_cases(repo, temp_dir)
        fails_ab, refused_ab = run_plant_cases_ab(repo, temp_dir)
        failures.extend(fails_ab)
        fails_sc, refused_sc = run_plant_case_scalar_unlisted(repo, temp_dir)
        failures.extend(fails_sc)
        fails_c, refused_c = run_plant_case_c(repo, temp_dir)
        failures.extend(fails_c)
        fails_de, refused_de = run_plant_cases_de(repo, temp_dir)
        failures.extend(fails_de)
        fails_f, refused_f = run_plant_case_f_convergence(repo, temp_dir)
        failures.extend(fails_f)
        fails_gh, accepted_gh = run_accepted_cases_gh(repo, temp_dir)
        failures.extend(fails_gh)
        fails_decl, refused_decl, accepted_decl = run_declared_cases(repo, generated)
        failures.extend(fails_decl)

        total_refused = (
            refused_rep
            + refused_ab
            + refused_sc
            + refused_c
            + refused_de
            + refused_f
            + refused_decl
        )
        total_accepted = accepted_rep + accepted_gh + accepted_decl
        return failures, total_refused, total_accepted


def report(failures: list[str]) -> bool:
    for failure in failures:
        print(f"FAIL: {failure}", file=sys.stderr)
    return bool(failures)


def run_selftest(root: Path) -> int:
    failures, refused, accepted = selftest(root)
    if report(failures):
        return 1
    print(f"PASS: regenerate selftest ({refused} plants refused, {accepted} accepted)")
    return 0


def run_check(root: Path) -> int:
    if report(check_declared(root)):
        return 1
    count = len(get_generated_files(root))
    print(f"PASS: regenerate check ({count} generated files, {DECLARED} equal)")
    return 0


def run_write(root: Path, out_arg: Path | None) -> int:
    if not out_arg:
        raise CheckError("--out DIR is required for write")
    if report(check_declared(root)):
        return 1
    failures, message = write_regenerated(out_arg.resolve(), root)
    if report(failures):
        return 1
    if message == "nothing to regenerate":
        print("PASS: nothing to regenerate")
    else:
        print(f"PASS: regenerate write ({message})")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=(__doc__ or "").splitlines()[0])
    parser.add_argument("command", choices=("write", "check", "selftest"))
    parser.add_argument("--out", type=Path, default=None)
    parser.add_argument(
        "--root", type=Path, default=Path(__file__).resolve().parents[2]
    )
    args = parser.parse_args()
    root = args.root.resolve()
    try:
        if args.command == "selftest":
            return run_selftest(root)
        if args.command == "check":
            return run_check(root)
        return run_write(root, args.out)
    except CheckError as error:
        print(f"ERROR: {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
