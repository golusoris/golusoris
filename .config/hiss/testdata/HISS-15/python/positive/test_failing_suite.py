# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""A failing unittest suite the Python runner must reject."""

import unittest


class FailingSuiteTest(unittest.TestCase):
    """Prove assertion failures propagate out of the runner."""

    def test_failure_is_blocking(self):
        self.assertEqual(1, 2)


if __name__ == "__main__":
    unittest.main()
