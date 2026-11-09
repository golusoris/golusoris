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
import sys
from pathlib import Path

MAX_REPORTS = 64
MAX_PRINTED_ERRORS = 5
MAX_PRINTED_FINDINGS = 10


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


def print_diagnostics(
    errors: list[object],
    results: list[object],
    missing: list[str],
) -> None:
    """Print the first few errors, findings, and unscanned files."""
    for error in errors[:MAX_PRINTED_ERRORS]:
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
    return parser.parse_args(argv[1:])


def validate_scanner_status(status: int) -> bool:
    """Verify that the scanner exit status was zero."""
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

    print_diagnostics(errors, results, missing)

    if errors or results or missing:
        return 1
    if not validate_scanner_status(args.scanner_status):
        return 1

    print(f"Semgrep custom rules: {len(scanned)} repository Go files passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
