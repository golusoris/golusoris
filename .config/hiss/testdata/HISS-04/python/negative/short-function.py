# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""A function inside every HISS-04 cap."""


def accumulate(values):
    """Short, single-branch, well inside the caps."""
    total = 0
    for value in values:
        total += value
    return total
