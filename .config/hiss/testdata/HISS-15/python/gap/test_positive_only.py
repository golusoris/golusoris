# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""A passing suite with only the positive test dimension."""

import unittest


def clamp(value, low, high):
    """Confine value to the closed interval."""
    return min(max(value, low), high)


class IncompleteClampTest(unittest.TestCase):
    """Show that unittest execution cannot decide test shape."""

    def test_positive_inside_interval(self):
        self.assertEqual(clamp(5, 0, 10), 5)


if __name__ == "__main__":
    unittest.main()
