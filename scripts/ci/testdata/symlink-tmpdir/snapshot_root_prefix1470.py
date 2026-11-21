# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2

"""Planted pre-fix shape of the 1470 snapshot test's unresolved temp root."""

import tempfile
import unittest
from pathlib import Path


class SnapshotRootTest(unittest.TestCase):
    def test_snapshot_root(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            driver = str(root.resolve(strict=True))
            expected = str(root)  # pre-fix 1470 expectation
            self.assertEqual(driver, expected)


if __name__ == "__main__":
    unittest.main()
