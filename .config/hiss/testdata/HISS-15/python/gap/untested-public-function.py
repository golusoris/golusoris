# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""A public interface with no test of any dimension."""


def clamp(value, low, high):
    """Confine value to [low, high]; nothing in the repository tests it."""
    if value < low:
        return low
    if value > high:
        return high
    return value
