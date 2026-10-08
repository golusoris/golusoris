# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""The same tree walk with an explicit, bounded stack: the call graph stays a DAG."""

MAX_NODES = 100_000


def walk(root):
    """Count nodes without recursion, refusing trees above MAX_NODES."""
    total = 0
    stack = [root]
    while stack:
        if total >= MAX_NODES:
            raise ValueError("tree exceeds MAX_NODES")
        node = stack.pop()
        total += 1
        stack.extend(node.get("children", []))
    return total
