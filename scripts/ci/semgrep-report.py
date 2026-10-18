#!/usr/bin/env python3

# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Judge the JSON report of scripts/ci/semgrep-scan.sh.

Usage: semgrep-report.py REPORT SELECTED_FILES [OPTIONS]

REPORT holds one JSON document per scanner run: xargs starts another scanner
once the command line is full. SELECTED_FILES is the NUL-separated list of
files the scan was asked to read. The script fails on an unreadable report, a
scanner error, a finding, or a selected file that no run scanned.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

MAX_REPORTS = 64
MAX_PRINTED_ERRORS = 5
MAX_PRINTED_FINDINGS = 10
TOLERATED_SCANNER_STATUS = 123
GENERIC_ALIAS_RE = re.compile(r"^\s*(?:type\s+)?[A-Za-z_]\w*\[[^\]]+\]\s*=\s*\S")


def load_reports(text: str) -> list[dict[str, object]]:
    """Decode the JSON documents the scanner runs wrote one after another."""
    decoder = json.JSONDecoder()
    reports: list[dict[str, object]] = []
    remaining = text.lstrip()
    for _ in range(MAX_REPORTS):
        if not remaining:
            break
        payload, end = decoder.raw_decode(remaining)
        if not isinstance(payload, dict):
            message = "a scanner report is not a JSON object"
            raise TypeError(message)
        reports.append(payload)
        remaining = remaining[end:].lstrip()
    if remaining:
        message = f"more than {MAX_REPORTS} scanner reports"
        raise ValueError(message)
    if not reports:
        message = "empty report"
        raise ValueError(message)
    return reports


def collect(reports: list[dict[str, object]], key: str) -> list[object]:
    """Join one list-valued field over every scanner run."""
    joined: list[object] = []
    for payload in reports:
        value = payload.get(key, [])
        if isinstance(value, list):
            joined.extend(value)
    return joined


def scanned_paths(reports: list[dict[str, object]]) -> set[str]:
    """Return every path any scanner run reports as scanned."""
    scanned: set[str] = set()
    for payload in reports:
        paths = payload.get("paths", {})
        listed = paths.get("scanned", []) if isinstance(paths, dict) else []
        scanned.update(Path(str(path)).as_posix() for path in listed)
    return scanned


def print_finding(result: object) -> None:
    """Print one finding as path:line: rule: message."""
    if not isinstance(result, dict):
        print(f"Semgrep finding: {result}", file=sys.stderr)
        return
    start = result.get("start", {})
    extra = result.get("extra", {})
    line = start.get("line", 0) if isinstance(start, dict) else 0
    message = extra.get("message", "finding") if isinstance(extra, dict) else "finding"
    path = result.get("path", "unknown")
    check = result.get("check_id", "unknown")
    print(f"{path}:{line}: {check}: {message}", file=sys.stderr)


def is_span_on_generic_alias(
    span: object,
    exception_path: str,
    source_lines: list[str],
) -> bool:
    """Verify that a span targets the exception file and an alias declaration."""
    if not isinstance(span, dict):
        return False
    target = span.get("file") or span.get("path")
    if Path(str(target or "")).as_posix() != Path(exception_path).as_posix():
        return False
    start = span.get("start")
    line_no = start.get("line") if isinstance(start, dict) else None
    if not isinstance(line_no, int) or not (1 <= line_no <= len(source_lines)):
        return False
    return bool(GENERIC_ALIAS_RE.match(source_lines[line_no - 1]))


def is_tolerated_error(
    error: dict[str, object],
    exception_path: str,
    stage_dir: Path | None,
) -> bool:
    """Check if a Semgrep error matches the tolerated generic alias parse failure."""
    if stage_dir is None:
        return False
    err_type = error.get("type")
    if not (isinstance(err_type, list) and err_type and err_type[0] == "PartialParsing"):
        return False
    err_path = error.get("path")
    if Path(str(err_path or "")).as_posix() != Path(exception_path).as_posix():
        return False
    spans = error.get("spans")
    if not isinstance(spans, list) or not spans:
        return False
    source_file = stage_dir / exception_path
    if not source_file.is_file():
        return False
    try:
        source_lines = source_file.read_text(encoding="utf-8").splitlines()
    except OSError:
        return False
    return all(
        is_span_on_generic_alias(span, exception_path, source_lines)
        for span in spans
    )


def partition_errors(
    errors: list[object],
    exception_path: str | None,
    stage_dir: Path | None,
) -> tuple[list[dict[str, object]], list[object]]:
    """Split errors into tolerated generic alias failures and active errors."""
    tolerated: list[dict[str, object]] = []
    active: list[object] = []
    for err in errors:
        if (
            isinstance(err, dict)
            and exception_path is not None
            and is_tolerated_error(err, exception_path, stage_dir)
        ):
            tolerated.append(err)
        else:
            active.append(err)
    return tolerated, active


def print_diagnostics(
    active_errors: list[object],
    results: list[object],
    missing: list[str],
) -> None:
    """Print the first few active errors, findings, and unscanned files."""
    for error in active_errors[:MAX_PRINTED_ERRORS]:
        print(f"Semgrep error: {error}", file=sys.stderr)
    for result in results[:MAX_PRINTED_FINDINGS]:
        print_finding(result)
    for path in missing[:MAX_PRINTED_FINDINGS]:
        print(f"Semgrep did not scan selected file: {path}", file=sys.stderr)


def read_expected_paths(expected_file: Path) -> set[str]:
    """Read the expected set of scanned files from the NUL-separated list."""
    return {
        Path(raw.decode("utf-8")).as_posix()
        for raw in expected_file.read_bytes().split(b"\0")
        if raw
    }


def parse_arguments(argv: list[str]) -> argparse.Namespace:
    """Parse command-line arguments for the report validator."""
    parser = argparse.ArgumentParser(
        description="Judge the JSON report of scripts/ci/semgrep-scan.sh.",
    )
    parser.add_argument("report", type=Path, help="Path to Semgrep JSON report")
    parser.add_argument(
        "expected", type=Path, help="Path to NUL-separated selected files"
    )
    parser.add_argument(
        "--scanner-status", type=int, default=0, help="Scanner exit status"
    )
    parser.add_argument(
        "--stage-dir", type=Path, default=None, help="Stage directory with sources"
    )
    parser.add_argument(
        "--tolerate-alias-parse",
        type=str,
        default=None,
        help="Path where generic alias parse errors are tolerated",
    )
    parser.add_argument(
        "--alias-parse-expires",
        type=str,
        default=None,
        help="Expiry date (YYYY-MM-DD) for generic alias tolerance",
    )
    return parser.parse_args(argv[1:])


def validate_scanner_status(status: int, tolerated_count: int) -> bool:
    """Verify that the scanner exit status matches the tolerated error count."""
    if status == TOLERATED_SCANNER_STATUS:
        if tolerated_count == 0:
            print(
                f"Semgrep scanner exited with status {status} without errors",
                file=sys.stderr,
            )
            return False
        return True
    if status != 0:
        print(f"Semgrep scanner failed with exit status {status}", file=sys.stderr)
        return False
    return True


def main(argv: list[str]) -> int:
    """Read the report and the selection; return the exit status."""
    try:
        args = parse_arguments(argv)
    except SystemExit as exc:
        return exc.code if isinstance(exc.code, int) else 2
    if not args.report.is_file():
        print("Semgrep did not produce its JSON report", file=sys.stderr)
        return 1
    try:
        reports = load_reports(args.report.read_text(encoding="utf-8"))
    except (OSError, ValueError, TypeError) as error:
        print(f"Semgrep report is unreadable: {error}", file=sys.stderr)
        return 1

    errors = collect(reports, "errors")
    results = collect(reports, "results")
    expected = read_expected_paths(args.expected)
    scanned = scanned_paths(reports)
    missing = sorted(expected - scanned)

    tolerated, active = partition_errors(
        errors, args.tolerate_alias_parse, args.stage_dir
    )
    print_diagnostics(active, results, missing)

    if active or results or missing:
        return 1
    if not validate_scanner_status(args.scanner_status, len(tolerated)):
        return 1

    if tolerated:
        expires = args.alias_parse_expires or "expiry"
        print(
            f"Semgrep tolerates {len(tolerated)} parse error(s) in "
            f"{args.tolerate_alias_parse} until {expires} (semgrep/semgrep#11972)"
        )
    print(f"Semgrep custom rules: {len(scanned)} repository Go files passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
