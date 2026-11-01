#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Detect review bots active on a GitHub repository and render their README badges.

Usage:
  review_bot_badges.py OWNER/REPO                 print one badge line per active bot
  review_bot_badges.py OWNER/REPO --check FILE    exit 1 when FILE lacks an active bot's badge

Detection reads each service's own answer, never a per-repository list:
CodeRabbit = shields endpoint knows the repository; Codacy = public API lists it
as added and returns its grade badge. Exit codes: 0 ok, 1 badge missing,
3 error (unreachable service, unexpected answer, bad argument, unreadable file).
"""

from __future__ import annotations

import argparse
import http.client
import json
import re
import ssl
import sys
import urllib.parse
from collections.abc import Callable
from pathlib import Path

TIMEOUT_SECONDS = 15
MAX_BODY_BYTES = 1 << 20
REPO_RE = re.compile(r"^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$")
# shields renders the count with metric(): 999, 1.2k, 10k, 1M.
CODERABBIT_ACTIVE_RE = re.compile(r"^coderabbit reviews: \d+(?:\.\d)?[kMGTPEZY]?$")
CODERABBIT_INACTIVE = "coderabbit reviews: provider or repo not found"
CODACY_GRADE_PREFIX = "https://app.codacy.com/project/badge/Grade/"
# Codacy API v3 AddedState enum.
CODACY_ACTIVE_STATES = frozenset({"Added", "Following"})
CODACY_INACTIVE_STATES = frozenset({"NotAdded"})
EXIT_MISSING = 1
EXIT_ERROR = 3

Fetch = Callable[[str], tuple[int, str, str]]


class DetectionError(Exception):
    """A service answered in a way that proves neither active nor inactive."""


def fetch(url: str) -> tuple[int, str, str]:
    """Return status, content type and body; network errors and non-https URLs map to status 0."""
    parts = urllib.parse.urlsplit(url)
    if parts.scheme != "https" or not parts.hostname:
        return 0, "", ""
    conn = http.client.HTTPSConnection(  # nosemgrep: python.lang.security.audit.httpsconnection-detected.httpsconnection-detected -- Python 3.12+ with an explicit verifying default context
        parts.hostname, timeout=TIMEOUT_SECONDS, context=ssl.create_default_context()
    )
    try:
        conn.request(
            "GET",
            parts.path or "/",
            headers={"User-Agent": "golusoris-review-bot-badges"},
        )
        resp = conn.getresponse()
        body = resp.read(MAX_BODY_BYTES).decode("utf-8", "replace")
        return resp.status, resp.getheader("Content-Type", ""), body
    except (OSError, http.client.HTTPException):
        return 0, "", ""
    finally:
        conn.close()


def coderabbit(repo: str, get: Fetch) -> str | None:
    url = f"https://img.shields.io/coderabbit/prs/github/{repo}"
    status, _, body = get(url)
    match = re.search(r"<title>([^<]*)</title>", body)
    title = match.group(1) if match else ""
    if status == 200 and CODERABBIT_ACTIVE_RE.match(title):
        return f"[![CodeRabbit reviews]({url})](https://coderabbit.ai)"
    if status == 200 and title == CODERABBIT_INACTIVE:
        return None
    raise DetectionError(
        f"coderabbit: unexpected answer: status {status}, title {title!r}"
    )


def codacy_data(repo: str, get: Fetch) -> dict | None:
    owner, name = repo.split("/", 1)
    status, _, body = get(
        f"https://app.codacy.com/api/v3/organizations/gh/{owner}/repositories/{name}"
    )
    if status == 404:
        return None
    if status != 200:
        raise DetectionError(f"codacy: unexpected status {status}")
    try:
        data = json.loads(body).get("data")
    except (ValueError, AttributeError) as err:
        raise DetectionError(f"codacy: malformed answer: {err}") from err
    if not isinstance(data, dict):
        raise DetectionError("codacy: answer has no data object")
    return data


def codacy(repo: str, get: Fetch) -> str | None:
    data = codacy_data(repo, get)
    if data is None:
        return None
    state = data.get("addedState")
    if not isinstance(state, str):
        raise DetectionError(f"codacy: addedState is not a string: {state!r}")
    if state in CODACY_INACTIVE_STATES:
        return None
    if state not in CODACY_ACTIVE_STATES:
        raise DetectionError(f"codacy: unknown addedState {state!r}")
    badges_obj = data.get("badges")
    grade = badges_obj.get("grade") if isinstance(badges_obj, dict) else None
    if not isinstance(grade, str) or not grade.startswith(CODACY_GRADE_PREFIX):
        raise DetectionError(f"codacy: added but no Codacy grade badge: {grade!r}")
    return f"[![Codacy grade]({grade})](https://app.codacy.com/gh/{repo}/dashboard)"


DETECTORS = (coderabbit, codacy)


def badges(repo: str, get: Fetch = fetch) -> list[str]:
    return [line for line in (detect(repo, get) for detect in DETECTORS) if line]


def missing(readme: str, lines: list[str]) -> list[str]:
    return [line for line in lines if line not in readme]


def run(repo: str, check: Path | None, get: Fetch) -> int:
    readme = check.read_text(encoding="utf-8") if check is not None else None
    lines = badges(repo, get)
    if readme is None:
        print("\n".join(lines))
        return 0
    absent = missing(readme, lines)
    for line in absent:
        print(f"{check}: missing review bot badge: {line}", file=sys.stderr)
    return EXIT_MISSING if absent else 0


def main(argv: list[str], get: Fetch = fetch) -> int:
    parser = argparse.ArgumentParser(
        description="Detect review bots and render their README badges."
    )
    parser.add_argument("repo", help="OWNER/REPO on GitHub")
    parser.add_argument(
        "--check", type=Path, help="README that must carry every active bot badge"
    )
    try:
        args = parser.parse_args(argv)
    except SystemExit as exc:
        return EXIT_ERROR if exc.code else 0  # usage error -> 3; --help -> 0
    if not REPO_RE.match(args.repo):
        print(f"review_bot_badges: not OWNER/REPO: {args.repo}", file=sys.stderr)
        return EXIT_ERROR
    try:
        return run(args.repo, args.check, get)
    except (OSError, UnicodeError, DetectionError) as err:
        print(f"review_bot_badges: {err}", file=sys.stderr)
        return EXIT_ERROR


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
