# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""The same unbounded loop written with a truthy literal instead of True."""


def drain(queue):
    """Identical semantics to while True, matched by no rule in the scanner."""
    total = 0
    while 1:
        total += queue.get()
