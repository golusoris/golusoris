# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""A cycle in the call graph that no Python rule decides."""


def walk(node):
    """Recurse into every child, so the call graph is not a DAG."""
    total = 1
    for child in node.get("children", []):
        total += walk(child)
    return total
