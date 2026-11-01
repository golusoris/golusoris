#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Unit tests for review_bot_badges.py; probes are stubbed, no network."""

from __future__ import annotations

import contextlib
import io
import json
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import review_bot_badges as rbb

REPO = "acme/widget"
GRADE = "https://app.codacy.com/project/badge/Grade/0123456789abcdef0123456789abcdef"
SHIELDS = "https://img.shields.io/coderabbit"
CODACY = "https://app.codacy.com/api"


def stub(responses: dict[str, tuple[int, str, str]]) -> rbb.Fetch:
    """Return a fetch stub answering by URL prefix; unknown URLs look unreachable."""

    def get(url: str) -> tuple[int, str, str]:
        """Answer the first matching prefix, else status 0."""
        for prefix, answer in responses.items():
            if url.startswith(prefix):
                return answer
        return 0, "", ""

    return get


def shields(title: str, status: int = 200) -> tuple[int, str, str]:
    """Return a shields SVG answer carrying the given title."""
    return status, "image/svg+xml", f"<svg><title>{title}</title></svg>"


def codacy_answer(data: object, status: int = 200) -> tuple[int, str, str]:
    """Return a Codacy API JSON answer wrapping the given data object."""
    return status, "application/json", json.dumps({"data": data})


ALL_ACTIVE = {
    SHIELDS: shields("coderabbit reviews: 3"),
    CODACY: codacy_answer({"addedState": "Added", "badges": {"grade": GRADE}}),
}
INACTIVE = {
    SHIELDS: shields(rbb.CODERABBIT_INACTIVE),
    CODACY: (404, "", ""),
}


def run_main(argv: list[str], answers: dict[str, tuple[int, str, str]]) -> int:
    """Run main with stubbed services and silenced output; return its exit code."""
    with (
        contextlib.redirect_stdout(io.StringIO()),
        contextlib.redirect_stderr(io.StringIO()),
    ):
        return rbb.main(argv, stub(answers))


class DetectionTest(unittest.TestCase):
    def test_all_active_renders_two_badges(self) -> None:
        """Both services active yield both badge lines."""
        lines = rbb.badges(REPO, stub(ALL_ACTIVE))
        self.assertEqual(len(lines), 2)
        self.assertIn("coderabbit/prs/github/acme/widget", lines[0])
        self.assertIn(GRADE, lines[1])

    def test_unknown_repository_renders_nothing(self) -> None:
        """Both services not knowing the repository yield no badges."""
        self.assertEqual(rbb.badges(REPO, stub(INACTIVE)), [])

    def test_codacy_not_added_renders_nothing(self) -> None:
        """Codacy NotAdded counts as inactive."""
        answers = dict(
            ALL_ACTIVE, **{CODACY: codacy_answer({"addedState": "NotAdded"})}
        )
        self.assertIsNone(rbb.codacy(REPO, stub(answers)))

    def test_codacy_following_counts_as_active(self) -> None:
        """Codacy Following counts as active."""
        data = {"addedState": "Following", "badges": {"grade": GRADE}}
        self.assertIn(
            GRADE, rbb.codacy(REPO, stub({CODACY: codacy_answer(data)})) or ""
        )

    def test_codacy_missing_or_unknown_state_is_error(self) -> None:
        """A missing, unknown or non-string addedState is a detection error."""
        for data in (
            {},
            {"addedState": "Pending"},
            {"addedState": None},
            {"addedState": ["NotAdded"]},
            {"addedState": {"state": "Added"}},
            {"addedState": 1},
        ):
            with self.subTest(data=data), self.assertRaises(rbb.DetectionError):
                rbb.codacy(REPO, stub({CODACY: codacy_answer(data)}))

    def test_shields_metric_counts_are_active(self) -> None:
        """Every shields metric() count form counts as active."""
        for count in ("0", "999", "1k", "1.2k", "10k", "999k", "1M", "1.5G"):
            answers = {SHIELDS: shields(f"coderabbit reviews: {count}")}
            with self.subTest(count=count):
                self.assertIsNotNone(rbb.coderabbit(REPO, stub(answers)))

    def test_codacy_foreign_or_absent_badge_is_error(self) -> None:
        """An added repository without a Codacy grade badge URL is an error."""
        for badges_obj in ({"grade": "https://evil.example/x"}, {}, "x", None):
            answers = {
                CODACY: codacy_answer({"addedState": "Added", "badges": badges_obj})
            }
            with self.subTest(badges=badges_obj), self.assertRaises(rbb.DetectionError):
                rbb.codacy(REPO, stub(answers))

    def test_codacy_malformed_answer_is_error(self) -> None:
        """Malformed JSON, a non-object body or an odd status is an error."""
        for answer in (
            (200, "application/json", "{not json"),
            (200, "application/json", "[]"),
            codacy_answer("not an object"),
            (500, "", ""),
        ):
            with self.subTest(answer=answer), self.assertRaises(rbb.DetectionError):
                rbb.codacy(REPO, stub({CODACY: answer}))

    def test_shields_error_badges_are_errors(self) -> None:
        """shields error titles, non-200 status and no title are errors."""
        for answer in (
            shields("coderabbit reviews: inaccessible"),
            shields("coderabbit reviews: rate limited by upstream service"),
            shields("coderabbit reviews: 3", status=503),
            (200, "image/svg+xml", "<svg/>"),
        ):
            with self.subTest(answer=answer), self.assertRaises(rbb.DetectionError):
                rbb.coderabbit(REPO, stub({SHIELDS: answer}))

    def test_unreachable_service_is_error(self) -> None:
        """An unreachable service is an error, never inactive."""
        with self.assertRaises(rbb.DetectionError):
            rbb.badges(REPO, stub({}))

    def test_fetch_refuses_non_https(self) -> None:
        """fetch never reads a non-https or hostless URL."""
        for url in ("file:///etc/passwd", "http://img.shields.io/x", "https:///nohost"):
            with self.subTest(url=url):
                self.assertEqual(rbb.fetch(url), (0, "", ""))

    def test_missing_lists_absent_lines(self) -> None:
        """missing lists exactly the lines absent from the README."""
        lines = rbb.badges(REPO, stub(ALL_ACTIVE))
        self.assertEqual(rbb.missing(lines[0], lines), [lines[1]])
        self.assertEqual(rbb.missing("\n".join(lines), lines), [])


class ExitCodeTest(unittest.TestCase):
    def setUp(self) -> None:
        """Create a temporary README path."""
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.readme = Path(self.tmp.name) / "README.md"

    def test_check_passes_with_all_badges(self) -> None:
        """A README with every badge exits 0."""
        self.readme.write_text(
            "\n".join(rbb.badges(REPO, stub(ALL_ACTIVE))), encoding="utf-8"
        )
        self.assertEqual(run_main([REPO, "--check", str(self.readme)], ALL_ACTIVE), 0)

    def test_check_missing_badge_exits_1(self) -> None:
        """A README lacking a badge exits 1."""
        self.readme.write_text("# widget\n", encoding="utf-8")
        self.assertEqual(
            run_main([REPO, "--check", str(self.readme)], ALL_ACTIVE), rbb.EXIT_MISSING
        )

    def test_unreachable_service_exits_3(self) -> None:
        """An unreachable service exits 3."""
        self.readme.write_text("# widget\n", encoding="utf-8")
        self.assertEqual(
            run_main([REPO, "--check", str(self.readme)], {}), rbb.EXIT_ERROR
        )

    def test_unreadable_readme_exits_3(self) -> None:
        """An absent README exits 3."""
        missing_file = str(Path(self.tmp.name) / "absent.md")
        self.assertEqual(
            run_main([REPO, "--check", missing_file], ALL_ACTIVE), rbb.EXIT_ERROR
        )

    def test_undecodable_readme_exits_3(self) -> None:
        """A README that is not UTF-8 exits 3."""
        self.readme.write_bytes(b"\xff\xfe\xfa not utf-8")
        self.assertEqual(
            run_main([REPO, "--check", str(self.readme)], ALL_ACTIVE), rbb.EXIT_ERROR
        )

    def test_bad_arguments_exit_3(self) -> None:
        """Bad repository or usage arguments exit 3."""
        for argv in (["acme/widget/../x"], [], ["a/b", "--bogus"]):
            with self.subTest(argv=argv):
                self.assertEqual(run_main(argv, ALL_ACTIVE), rbb.EXIT_ERROR)


if __name__ == "__main__":
    unittest.main()
