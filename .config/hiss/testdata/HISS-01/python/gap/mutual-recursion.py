# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""A two-function cycle; the line scanner decides only direct self-calls."""


def walk_even(node):
    """Hand every child to walk_odd, which hands its children back."""
    total = 1
    for child in node.get("children", []):
        total += walk_odd(child)
    return total


def walk_odd(node):
    """Close the cycle walk_even -> walk_odd -> walk_even."""
    total = 1
    for child in node.get("children", []):
        total += walk_even(child)
    return total
