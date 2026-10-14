#!/usr/bin/env python3
# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Positive, negative, and boundary tests for the HISS-20 claim-count check."""

from __future__ import annotations

from unittest import TestCase, main

import hiss_coverage_counts as counts

REPLAYED = "      - language: go\n        state: partial\n"
DELEGATED = "      - language: go\n        state: partial\n        runner: ci\n"


def catalogue(claims: str, stated: tuple[int, int, int]) -> str:
    """Build a two-invariant catalogue whose HISS-20 rationale states the given counts."""
    total, replayed, delegated = stated
    return (
        "rules:\n  - id: HISS-01\n    coverage:\n"
        f"{claims}"
        "  - id: HISS-20\n    coverage:\n"
        f"{DELEGATED}"
        "        rationale: >-\n"
        f"          First, of the {total}\n"
        f"          claims declared here only {replayed} are replayed by the verifier; the other {delegated} name a tool\n"
    )


class CheckTest(TestCase):
    """The stated counts must equal the measured ones."""

    def test_repository_catalogue_matches(self) -> None:
        self.assertEqual(counts.check(counts.CATALOGUE.read_text(encoding="utf-8")), [])

    def test_matching_counts_pass(self) -> None:
        self.assertEqual(counts.check(catalogue(REPLAYED + DELEGATED, (3, 1, 2))), [])

    def test_added_replayed_claim_fails(self) -> None:
        errors = counts.check(catalogue(REPLAYED * 2 + DELEGATED, (3, 1, 2)))
        self.assertEqual(len(errors), 1)
        self.assertIn(
            "catalogue has Counts(claims=4, replayed=2, delegated=2)", errors[0]
        )

    def test_added_delegated_claim_fails(self) -> None:
        errors = counts.check(catalogue(REPLAYED + DELEGATED * 2, (3, 1, 2)))
        self.assertIn("delegated=3", errors[0])

    def test_stale_statement_fails(self) -> None:
        self.assertEqual(
            len(counts.check(catalogue(REPLAYED + DELEGATED, (33, 22, 11)))), 1
        )

    def test_missing_statement_fails_closed(self) -> None:
        text = catalogue(REPLAYED, (2, 1, 1)).replace("the other", "and")
        self.assertEqual(
            counts.check(text), ["expected one stated claim count, found 0"]
        )

    def test_second_statement_fails_closed(self) -> None:
        text = catalogue(REPLAYED, (2, 1, 1))
        rationale = text[text.index("          First") :]
        self.assertEqual(
            counts.check(text + rationale),
            ["expected one stated claim count, found 2"],
        )

    def test_claim_outside_invariant_fails_closed(self) -> None:
        errors = counts.check(REPLAYED + catalogue(REPLAYED, (2, 1, 1)))
        self.assertEqual(errors, ["claim outside an invariant: - language: go"])

    def test_line_bound_is_inclusive(self) -> None:
        text = catalogue(REPLAYED, (2, 1, 1))
        padding = counts.MAX_LINES - len(text.splitlines())
        self.assertEqual(counts.check(text + "#\n" * padding), [])
        self.assertEqual(
            counts.check(text + "#\n" * (padding + 1)),
            [f"catalogue exceeds {counts.MAX_LINES} lines"],
        )


if __name__ == "__main__":
    main()
