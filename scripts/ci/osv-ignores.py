#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
"""Generate osv-scanner.toml files from .trivy/openvex.json and refuse drift.

OpenSSF Scorecard Vulnerabilities check reads osv-scanner.toml only in the
directory of each manifest (no propagation to child directories). Single source
of truth for exceptions is .trivy/openvex.json.

    write       generate/update osv-scanner.toml files and delete obsolete ones
    check       refuse drift (missing, extra, differing content, expired VEX)
    selftest    verify plant refusals and accepted cases on a fixture repository

Exit status: 0 success or no drift, 1 drift or check failure, 2 tool error.
"""

from __future__ import annotations

import argparse
import datetime
import json
import os
import re
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any

VEX_PATH = Path(".trivy/openvex.json")
DEFAULT_TIMEOUT_SECONDS = 60
GIT_TIMEOUT_SECONDS = 60
MODULES_DISCOVERY_TIMEOUT_SECONDS = 60
MAX_MODULES = 64


class CheckError(Exception):
    """Raised when osv-ignores generation or verification cannot proceed."""


def parse_expiry(status_notes: str | None) -> datetime.date | None:
    """Parse expiry date from status_notes (e.g. 'expires: YYYY-MM-DD')."""
    if not status_notes:
        return None
    match = re.search(r"expires:\s*(\d{4}-\d{2}-\d{2})", status_notes)
    if not match:
        return None
    try:
        return datetime.date.fromisoformat(match.group(1))
    except ValueError:
        return None


def first_sentence(text: str) -> str:
    """Extract the first sentence from text, ending with a period."""
    s = text.strip()
    match = re.search(r"\.\s", s)
    if match:
        return s[: match.start() + 1]
    return s


def escape_toml_string(text: str) -> str:
    """Escape backslashes and double quotes for TOML basic string literal."""
    return (
        text.replace("\\", "\\\\")
        .replace('"', '\\"')
        .replace("\n", "\\n")
        .replace("\r", "\\r")
        .replace("\t", "\\t")
    )


def parse_require_line(line: str) -> tuple[str | None, bool]:
    """Parse a require line outside a block and return (pkg, starts_block)."""
    parts = line.split()
    if len(parts) >= 2 and parts[1].startswith("("):
        return None, True
    if len(parts) == 1 and parts[0].startswith("require("):
        return None, True
    if len(parts) >= 2:
        return parts[1], False
    return None, False


def parse_go_mod_requires(content: str) -> set[str]:
    """Parse require paths from go.mod content (both single-line and blocks)."""
    requires: set[str] = set()
    in_block = False
    for raw_line in content.splitlines():
        line = raw_line.strip()
        if not line or line.startswith("//"):
            continue
        if in_block:
            if line == ")":
                in_block = False
            elif not line.startswith("//"):
                requires.add(line.split()[0])
        elif line.startswith("require"):
            pkg, in_block = parse_require_line(line)
            if pkg:
                requires.add(pkg)
    return requires


def parse_npm_package_name(purl: str) -> str:
    """Extract npm package name from purl, supporting scoped packages."""
    rest = purl.removeprefix("pkg:npm/")
    if rest.startswith(("@", "%40")):
        rest = rest.replace("%40", "@", 1)
        if "@" in rest[1:]:
            idx = rest.index("@", 1)
            return rest[:idx]
        return rest
    return rest.split("@", 1)[0]


def run_cmd(
    cmd: list[str], cwd: Path, timeout: int = DEFAULT_TIMEOUT_SECONDS
) -> str:
    """Execute a subprocess and return stripped stdout or raise CheckError."""
    try:
        proc = subprocess.run(  # noqa: S603 - fixed command on repository scripts and tools.
            cmd,
            cwd=cwd,
            capture_output=True,
            text=True,
            timeout=timeout,
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        raise CheckError(f"{' '.join(cmd)} failed to run: {error}") from error
    if proc.returncode != 0:
        err = proc.stderr.strip() or proc.stdout.strip()
        raise CheckError(f"{' '.join(cmd)} exited {proc.returncode}: {err}")
    return proc.stdout


def discover_modules(
    root: Path, timeout: int = MODULES_DISCOVERY_TIMEOUT_SECONDS
) -> list[str]:
    """Discover Go module paths in the repository using go-modules.sh."""
    script = root / "scripts" / "ci" / "go-modules.sh"
    if not script.is_file():
        raise CheckError(f"module discovery script not found: {script}")
    stdout = run_cmd(["bash", str(script), "list"], root, timeout)
    lines = [line.strip() for line in stdout.splitlines() if line.strip()]
    if not lines or len(lines) > MAX_MODULES:
        raise CheckError(f"unexpected module count: {len(lines)}")
    return lines


def find_tracked_package_locks(
    root: Path, timeout: int = GIT_TIMEOUT_SECONDS
) -> list[str]:
    """Find git-tracked package-lock.json files."""
    stdout = run_cmd(
        ["git", "ls-files", "-z", "--", "package-lock.json", "**/package-lock.json"],
        root,
        timeout,
    )
    tokens = stdout.split("\0")
    return [t.strip() for t in tokens if t.strip()]


def target_dirs_for_golang(
    root: Path, module_name: str, modules: list[str]
) -> list[str]:
    """Return module directories requiring module_name."""
    matching_dirs: list[str] = []
    for mod_dir in modules:
        mod_file = root / "go.mod" if mod_dir == "." else root / mod_dir / "go.mod"
        if not mod_file.is_file():
            continue
        try:
            content = mod_file.read_text(encoding="utf-8")
        except OSError as error:
            raise CheckError(f"could not read {mod_file}: {error}") from error
        if module_name in parse_go_mod_requires(content):
            matching_dirs.append(mod_dir)
    return matching_dirs


def target_dirs_for_npm(
    root: Path, package_name: str, lock_paths: list[str]
) -> list[str]:
    """Return directories containing package-lock.json with package_name."""
    matching_dirs: list[str] = []
    target_suffix = f"node_modules/{package_name}"
    for lock_path in lock_paths:
        lock_file = root / lock_path
        try:
            data = json.loads(lock_file.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError) as error:
            raise CheckError(f"could not parse {lock_file}: {error}") from error
        packages = data.get("packages", {})
        if any(k == target_suffix or k.endswith("/" + target_suffix) for k in packages):
            parent = Path(lock_path).parent.as_posix()
            matching_dirs.append("." if parent == "" else parent)
    return matching_dirs


# REUSE-IgnoreStart
SPDX_TOML_HEADER = (
    "# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>\n"
    "#\n"
    "# SPDX-License-Identifier: EUPL-1.2\n"
    "\n"
    "# Generated by scripts/ci/osv-ignores.py from .trivy/openvex.json; do not edit."
)
# REUSE-IgnoreEnd


def render_osv_toml(entries: list[dict[str, Any]]) -> str:
    """Render osv-scanner.toml contents from sorted IgnoredVulns entries."""
    lines = [SPDX_TOML_HEADER]
    sorted_entries = sorted(entries, key=lambda e: e["id"])
    for entry in sorted_entries:
        lines.append("")
        lines.append("[[IgnoredVulns]]")
        lines.append(f'id = "{escape_toml_string(entry["id"])}"')
        lines.append(f'ignoreUntil = {entry["ignoreUntil"]}')
        reason = f"See .trivy/openvex.json: {first_sentence(entry['impact'])}"
        lines.append(f'reason = "{escape_toml_string(reason)}"')
    lines.append("")
    return "\n".join(lines)


def find_existing_osv_scanner_files(root: Path) -> list[Path]:
    """Find all existing osv-scanner.toml files ignoring internal dirs."""
    found: list[Path] = []
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [
            d for d in dirnames
            if d not in {".git", ".workingdir", "node_modules"}
        ]
        if "osv-scanner.toml" in filenames:
            found.append(Path(dirpath) / "osv-scanner.toml")
    return sorted(found)


def collect_statement_vuln_ids(statement: dict[str, Any]) -> list[str]:
    """Collect vulnerability name and aliases as unique IDs."""
    vuln = statement.get("vulnerability", {})
    name = vuln.get("name")
    aliases = vuln.get("aliases", [])
    ids: list[str] = []
    if name:
        ids.append(name)
    for alias in aliases:
        if alias and alias not in ids:
            ids.append(alias)
    return ids


def resolve_product_dirs(
    root: Path,
    purl: str,
    modules: list[str],
    locks: list[str],
) -> list[str]:
    """Find target directories affected by a product purl."""
    if purl.startswith("pkg:golang/"):
        module_name = purl.removeprefix("pkg:golang/").split("@", 1)[0]
        return target_dirs_for_golang(root, module_name, modules)
    if purl.startswith("pkg:npm/"):
        pkg_name = parse_npm_package_name(purl)
        return target_dirs_for_npm(root, pkg_name, locks)
    raise CheckError(f"unsupported product purl: {purl}")


def record_dir_entry(
    existing: list[dict[str, Any]],
    vuln_id: str,
    expiry_iso: str,
    impact: str,
) -> None:
    """Record an IgnoredVulns entry if not already present."""
    if not any(e["id"] == vuln_id for e in existing):
        existing.append(
            {"id": vuln_id, "ignoreUntil": expiry_iso, "impact": impact}
        )


def add_statement_entries(
    dir_entries: dict[str, list[dict[str, Any]]],
    root: Path,
    stmt: dict[str, Any],
    modules: list[str],
    locks: list[str],
) -> None:
    """Extract and add entries from one VEX statement into dir_entries."""
    if stmt.get("status") != "not_affected":
        return
    expiry = parse_expiry(stmt.get("status_notes"))
    if expiry is None:
        return
    ids = collect_statement_vuln_ids(stmt)
    impact = stmt.get("impact_statement", "")
    for prod in stmt.get("products", []):
        dirs = resolve_product_dirs(root, prod.get("@id", ""), modules, locks)
        for d in dirs:
            existing = dir_entries.setdefault(d, [])
            for vuln_id in ids:
                record_dir_entry(existing, vuln_id, expiry.isoformat(), impact)


def build_expected_files(root: Path) -> dict[str, str]:
    """Build mapping of relative path to expected osv-scanner.toml content."""
    vex_file = root / VEX_PATH
    if not vex_file.is_file():
        raise CheckError(f"OpenVEX file not found: {vex_file}")
    try:
        vex_data = json.loads(vex_file.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise CheckError(f"could not read {vex_file}: {error}") from error

    modules = discover_modules(root)
    locks = find_tracked_package_locks(root)
    dir_entries: dict[str, list[dict[str, Any]]] = {}

    for stmt in vex_data.get("statements", []):
        add_statement_entries(dir_entries, root, stmt, modules, locks)

    expected: dict[str, str] = {}
    for d, entries in dir_entries.items():
        if entries:
            rel = "osv-scanner.toml" if d == "." else f"{d}/osv-scanner.toml"
            expected[rel] = render_osv_toml(entries)
    return expected


def check_vex_expiry(root: Path, today: datetime.date) -> list[str]:
    """Verify that no VEX statement with expiry has expired before today."""
    vex_file = root / VEX_PATH
    if not vex_file.is_file():
        return [f"OpenVEX file not found: {vex_file}"]
    try:
        vex_data = json.loads(vex_file.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        return [f"could not read {vex_file}: {error}"]

    failures: list[str] = []
    for stmt in vex_data.get("statements", []):
        if stmt.get("status") != "not_affected":
            continue
        expiry = parse_expiry(stmt.get("status_notes"))
        if expiry is not None and expiry < today:
            vuln_name = stmt.get("vulnerability", {}).get("name", "unknown")
            failures.append(
                f"VEX statement {vuln_name} expired on {expiry} (today {today})"
            )
    return failures


def check_drift(root: Path, today: datetime.date | None = None) -> list[str]:
    """Check for drift against expected osv-scanner.toml and expired VEX."""
    ref_today = datetime.date.today() if today is None else today
    failures: list[str] = check_vex_expiry(root, ref_today)
    expected = build_expected_files(root)
    existing_files = {
        p.relative_to(root).as_posix(): p
        for p in find_existing_osv_scanner_files(root)
    }

    for rel_path in sorted(expected):
        if rel_path not in existing_files:
            failures.append(f"missing osv-scanner.toml: {rel_path}")
        else:
            actual = existing_files[rel_path].read_text(encoding="utf-8")
            if actual != expected[rel_path]:
                failures.append(f"osv-scanner.toml content differs: {rel_path}")

    failures.extend(
        f"unexpected osv-scanner.toml: {rel_path}"
        for rel_path in sorted(existing_files)
        if rel_path not in expected
    )

    return failures


def write_osv_ignores(root: Path) -> tuple[int, int]:
    """Write expected osv-scanner.toml files and delete obsolete ones."""
    expected = build_expected_files(root)
    existing_files = {
        p.relative_to(root).as_posix(): p
        for p in find_existing_osv_scanner_files(root)
    }
    deleted = 0
    written = 0
    for rel_path, path in existing_files.items():
        if rel_path not in expected:
            path.unlink()
            deleted += 1
    for rel_path, content in expected.items():
        target = root / rel_path
        target.parent.mkdir(parents=True, exist_ok=True)
        if not target.is_file() or target.read_text(encoding="utf-8") != content:
            target.write_text(content, encoding="utf-8")
        written += 1
    return written, deleted


def setup_fixture_manifests(repo: Path) -> None:
    """Create fixture go.mod files, package-lock.json, and go-modules.sh."""
    (repo / "a").mkdir()
    (repo / "a" / "go.mod").write_text(
        "module a\n\ngo 1.22\n\nrequire example.com/x v1.0.0\n", encoding="utf-8"
    )
    (repo / "b").mkdir()
    (repo / "b" / "go.mod").write_text("module b\n\ngo 1.22\n", encoding="utf-8")

    (repo / "npm_pkg").mkdir()
    lock_data = {
        "name": "fixture-pkg",
        "version": "1.0.0",
        "lockfileVersion": 3,
        "packages": {
            "": {"name": "fixture-pkg", "version": "1.0.0"},
            "node_modules/braces": {"version": "3.0.3"},
        },
    }
    (repo / "npm_pkg" / "package-lock.json").write_text(
        json.dumps(lock_data, indent=2), encoding="utf-8"
    )

    (repo / "scripts" / "ci").mkdir(parents=True)
    script = repo / "scripts" / "ci" / "go-modules.sh"
    script.write_text(
        '#!/usr/bin/env bash\nset -euo pipefail\nif [ "${1:-}" = "list" ]; then\n  printf "a\\nb\\n"\nfi\n',
        encoding="utf-8",
    )
    script.chmod(0o755)


def setup_fixture_vex(repo: Path) -> None:
    """Create fixture .trivy/openvex.json document."""
    (repo / ".trivy").mkdir()
    vex_data = {
        "@context": "https://openvex.dev/ns/v0.2.0",
        "@id": "https://example.com/vex/fixture",
        "author": "https://example.com",
        "timestamp": "2026-10-15T00:00:00Z",
        "version": 1,
        "statements": [
            {
                "vulnerability": {
                    "name": "CVE-FIXTURE-1",
                    "aliases": ["GHSA-fixture-1"],
                },
                "products": [{"@id": "pkg:golang/example.com/x@v1.0.0"}],
                "status": "not_affected",
                "justification": "vulnerable_code_not_present",
                "impact_statement": "Fixture statement for Go module x. Additional context.",
                "status_notes": "expires: 2026-12-31",
            },
            {
                "vulnerability": {"name": "CVE-FIXTURE-2"},
                "products": [{"@id": "pkg:npm/braces@3.0.3"}],
                "status": "not_affected",
                "justification": "vulnerable_code_not_present",
                "impact_statement": "Fixture statement for npm braces. Extra context.",
                "status_notes": "expires: 2026-12-31",
            },
        ],
    }
    (repo / ".trivy" / "openvex.json").write_text(
        json.dumps(vex_data, indent=2), encoding="utf-8"
    )


def setup_selftest_fixture(temp_dir: Path) -> Path:
    """Build a minimal repository fixture with modules and package lock."""
    repo = temp_dir / "repo"
    repo.mkdir()
    run_cmd(["git", "init"], repo)
    run_cmd(["git", "config", "user.name", "test"], repo)
    run_cmd(["git", "config", "user.email", "test@example.com"], repo)
    setup_fixture_manifests(repo)
    setup_fixture_vex(repo)
    run_cmd(["git", "add", "."], repo)
    return repo


def run_selftest_plants_1_to_3(
    repo: Path, test_today: datetime.date
) -> tuple[list[str], int]:
    """Execute plant cases 1 to 3 and return failures with refused count."""
    failures: list[str] = []
    refused = 0

    target_a = repo / "a" / "osv-scanner.toml"
    orig_a = target_a.read_text(encoding="utf-8")
    plant1 = (
        orig_a
        + '\n[[IgnoredVulns]]\nid = "CVE-EXTRA"\nignoreUntil = 2026-12-31\nreason = "plant"\n'
    )
    target_a.write_text(plant1, encoding="utf-8")
    if not check_drift(repo, today=test_today):
        failures.append("plant 1 (extra IgnoredVulns) was accepted")
    else:
        refused += 1
    target_a.write_text(orig_a, encoding="utf-8")

    plant2 = "\n".join(
        line for line in orig_a.splitlines() if "GHSA-fixture-1" not in line
    )
    target_a.write_text(plant2, encoding="utf-8")
    if not check_drift(repo, today=test_today):
        failures.append("plant 2 (missing entry) was accepted")
    else:
        refused += 1
    target_a.write_text(orig_a, encoding="utf-8")

    target_b = repo / "b" / "osv-scanner.toml"
    target_b.write_text(orig_a, encoding="utf-8")
    if not check_drift(repo, today=test_today):
        failures.append("plant 3 (extra file in dir b) was accepted")
    else:
        refused += 1
    target_b.unlink()

    return failures, refused


def run_selftest_plants_4_to_6(
    repo: Path, test_today: datetime.date
) -> tuple[list[str], int]:
    """Execute plant cases 4 to 6 and return failures with refused count."""
    failures: list[str] = []
    refused = 0

    target_a = repo / "a" / "osv-scanner.toml"
    orig_a = target_a.read_text(encoding="utf-8")
    target_a.unlink()
    if not check_drift(repo, today=test_today):
        failures.append("plant 4 (deleted file) was accepted")
    else:
        refused += 1
    target_a.write_text(orig_a, encoding="utf-8")

    future_today = datetime.date(2027, 1, 1)
    if not check_drift(repo, today=future_today):
        failures.append("plant 5 (expired VEX entry) was accepted")
    else:
        refused += 1

    vex_file = repo / ".trivy" / "openvex.json"
    orig_vex = vex_file.read_text(encoding="utf-8")
    vex_data = json.loads(orig_vex)
    vex_data["statements"] = [vex_data["statements"][0]]
    vex_file.write_text(json.dumps(vex_data, indent=2), encoding="utf-8")
    if not check_drift(repo, today=test_today):
        failures.append("plant 6 (VEX statement removed) was accepted")
    else:
        refused += 1
    vex_file.write_text(orig_vex, encoding="utf-8")

    return failures, refused


def selftest(root: Path | None = None) -> tuple[list[str], int, int]:
    """Execute selftest on a fixture repo with proven negative and positive cases."""
    with tempfile.TemporaryDirectory(prefix="golusoris-osv-selftest-") as tmp:
        repo = setup_selftest_fixture(Path(tmp))
        test_today = datetime.date(2026, 10, 18)

        write_osv_ignores(repo)
        fails = check_drift(repo, today=test_today)
        accepted = 0
        failures: list[str] = []
        if fails:
            failures.append(f"positive write-then-check failed: {fails}")
        else:
            accepted += 1

        fails_1_3, refused_1_3 = run_selftest_plants_1_to_3(repo, test_today)
        failures.extend(fails_1_3)

        fails_4_6, refused_4_6 = run_selftest_plants_4_to_6(repo, test_today)
        failures.extend(fails_4_6)

        total_refused = refused_1_3 + refused_4_6
        return failures, total_refused, accepted


def report(failures: list[str]) -> bool:
    """Print each failure to stderr and return True if any were printed."""
    for failure in failures:
        print(f"FAIL: {failure}", file=sys.stderr)
    return bool(failures)


def run_selftest(root: Path) -> int:
    """Run the selftest suite."""
    failures, refused, accepted = selftest(root)
    if report(failures):
        return 1
    print(f"PASS: osv-ignores selftest ({refused} plants refused, {accepted} accepted)")
    return 0


def run_check(root: Path, today: datetime.date | None = None) -> int:
    """Check repository osv-scanner.toml files against openvex.json."""
    failures = check_drift(root, today)
    if report(failures):
        return 1
    expected = build_expected_files(root)
    print(f"PASS: osv-ignores check ({len(expected)} files verified)")
    return 0


def run_write(root: Path) -> int:
    """Write generated osv-scanner.toml files to the repository."""
    written, deleted = write_osv_ignores(root)
    print(f"PASS: osv-ignores write ({written} files written, {deleted} deleted)")
    return 0


def main() -> int:
    """Entry point for osv-ignores CLI."""
    parser = argparse.ArgumentParser(description=(__doc__ or "").splitlines()[0])
    parser.add_argument("command", choices=("write", "check", "selftest"))
    parser.add_argument(
        "--root", type=Path, default=Path(__file__).resolve().parents[2]
    )
    parser.add_argument(
        "--today", type=str, default=None, help="Override today date (YYYY-MM-DD)"
    )
    args = parser.parse_args()
    root = args.root.resolve()
    today = None
    if args.today:
        try:
            today = datetime.date.fromisoformat(args.today)
        except ValueError as err:
            print(f"ERROR: invalid --today format: {err}", file=sys.stderr)
            return 2
    try:
        if args.command == "selftest":
            return run_selftest(root)
        if args.command == "check":
            return run_check(root, today)
        return run_write(root)
    except CheckError as error:
        print(f"ERROR: {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
