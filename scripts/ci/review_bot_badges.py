#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Detect review bots active on a GitHub repository and render their README badges.

Usage:
  review_bot_badges.py OWNER/REPO                 print one badge line per active bot
  review_bot_badges.py OWNER/REPO --check FILE    exit 1 when FILE lacks an active bot's badge

Detection reads each service's own answer, never a per-repository list:
CodeRabbit = shields endpoint knows the repository; Codacy = public API lists it
as added and returns its grade badge.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
import urllib.error
import urllib.request
from collections.abc import Callable
from pathlib import Path

TIMEOUT_SECONDS = 15
MAX_BODY_BYTES = 1 << 20
REPO_RE = re.compile(r"^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$")

Fetch = Callable[[str], tuple[int, str, str]]


def fetch(url: str) -> tuple[int, str, str]:
    """Return status, content type and body; network errors map to status 0."""
    req = urllib.request.Request(url, headers={"User-Agent": "golusoris-review-bot-badges"})  # noqa: S310 - fixed https URLs
    try:
        with urllib.request.urlopen(req, timeout=TIMEOUT_SECONDS) as resp:  # noqa: S310 - fixed https URLs
            body = resp.read(MAX_BODY_BYTES).decode("utf-8", "replace")
            return resp.status, resp.headers.get("Content-Type", ""), body
    except urllib.error.HTTPError as err:
        return err.code, "", ""
    except (urllib.error.URLError, TimeoutError):
        return 0, "", ""


def coderabbit(repo: str, get: Fetch) -> str | None:
    status, _, body = get(f"https://img.shields.io/coderabbit/prs/github/{repo}")
    title = re.search(r"<title>([^<]*)</title>", body)
    if status != 200 or title is None or "not found" in title.group(1):
        return None
    return f"[![CodeRabbit reviews](https://img.shields.io/coderabbit/prs/github/{repo})](https://coderabbit.ai)"


def codacy(repo: str, get: Fetch) -> str | None:
    status, _, body = get(f"https://app.codacy.com/api/v3/organizations/gh/{repo.replace('/', '/repositories/', 1)}")
    if status != 200:
        return None
    data = json.loads(body).get("data", {})
    grade = data.get("badges", {}).get("grade", "")
    if data.get("addedState") != "Added" or not grade.startswith("https://app.codacy.com/project/badge/Grade/"):
        return None
    return f"[![Codacy grade]({grade})](https://app.codacy.com/gh/{repo}/dashboard)"


DETECTORS = (coderabbit, codacy)


def badges(repo: str, get: Fetch = fetch) -> list[str]:
    return [line for line in (detect(repo, get) for detect in DETECTORS) if line]


def missing(readme: str, lines: list[str]) -> list[str]:
    return [line for line in lines if line not in readme]


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description="Detect review bots and render their README badges.")
    parser.add_argument("repo", help="OWNER/REPO on GitHub")
    parser.add_argument("--check", type=Path, help="README that must carry every active bot badge")
    args = parser.parse_args(argv)
    if not REPO_RE.match(args.repo):
        parser.error(f"not OWNER/REPO: {args.repo}")
    lines = badges(args.repo)
    if args.check is None:
        print("\n".join(lines))
        return 0
    absent = missing(args.check.read_text(encoding="utf-8"), lines)
    for line in absent:
        print(f"{args.check}: missing review bot badge: {line}", file=sys.stderr)
    return 1 if absent else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
