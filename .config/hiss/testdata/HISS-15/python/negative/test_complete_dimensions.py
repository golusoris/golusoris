# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""Positive, negative, and boundary tests for one public interface."""

import unittest


def clamp(value, low, high):
    """Confine value to the closed interval."""
    return min(max(value, low), high)


class ClampTest(unittest.TestCase):
    """Exercise all required behavioral dimensions."""

    def test_positive_inside_interval(self):
        self.assertEqual(clamp(5, 0, 10), 5)

    def test_negative_below_interval(self):
        self.assertEqual(clamp(-1, 0, 10), 0)

    def test_boundary_at_upper_limit(self):
        self.assertEqual(clamp(10, 0, 10), 10)


if __name__ == "__main__":
    unittest.main()
