#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Unit tests for review_bot_badges.py; probes are stubbed, no network."""

from __future__ import annotations

import json
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import review_bot_badges as rbb

REPO = "acme/widget"
GRADE = "https://app.codacy.com/project/badge/Grade/0123456789abcdef0123456789abcdef"


def stub(responses: dict[str, tuple[int, str, str]]) -> rbb.Fetch:
    def get(url: str) -> tuple[int, str, str]:
        for prefix, answer in responses.items():
            if url.startswith(prefix):
                return answer
        return 0, "", ""

    return get


ALL_ACTIVE = {
    "https://img.shields.io/coderabbit": (200, "image/svg+xml", "<svg><title>coderabbit reviews: 3</title></svg>"),
    "https://app.codacy.com/api": (
        200,
        "application/json",
        json.dumps({"data": {"addedState": "Added", "badges": {"grade": GRADE}}}),
    ),
}


class ReviewBotBadgesTest(unittest.TestCase):
    def test_all_active_renders_two_badges(self) -> None:
        lines = rbb.badges(REPO, stub(ALL_ACTIVE))
        self.assertEqual(len(lines), 2)
        self.assertIn("coderabbit/prs/github/acme/widget", lines[0])
        self.assertIn(GRADE, lines[1])

    def test_unknown_repository_renders_nothing(self) -> None:
        inactive = {
            "https://img.shields.io/coderabbit": (
                200,
                "image/svg+xml",
                "<svg><title>coderabbit reviews: provider or repo not found</title></svg>",
            ),
            "https://app.codacy.com/api": (404, "", ""),
        }
        self.assertEqual(rbb.badges(REPO, stub(inactive)), [])

    def test_codacy_not_added_or_foreign_badge_refused(self) -> None:
        for data in ({"addedState": "Pending", "badges": {"grade": GRADE}}, {"addedState": "Added", "badges": {"grade": "https://evil.example/x"}}):
            answers = dict(ALL_ACTIVE)
            answers["https://app.codacy.com/api"] = (200, "application/json", json.dumps({"data": data}))
            self.assertIsNone(rbb.codacy(REPO, stub(answers)))

    def test_network_failure_renders_nothing(self) -> None:
        self.assertEqual(rbb.badges(REPO, stub({})), [])

    def test_check_reports_missing_badge(self) -> None:
        lines = rbb.badges(REPO, stub(ALL_ACTIVE))
        self.assertEqual(rbb.missing(lines[0], lines), [lines[1]])
        self.assertEqual(rbb.missing("\n".join(lines), lines), [])

    def test_repo_argument_shape_refused(self) -> None:
        with self.assertRaises(SystemExit):
            rbb.main(["acme/widget/../x"])


if __name__ == "__main__":
    unittest.main()
