# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""Direct recursion: the function body calls itself by its own name."""


def walk(node):
    """Recurse into every child, so the call graph is not a DAG."""
    total = 1
    for child in node.get("children", []):
        total += walk(child)
    return total
