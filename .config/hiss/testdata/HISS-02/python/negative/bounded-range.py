# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""The bounded equivalent of the positive fixture."""

MAX_DRAIN = 1024


def drain(queue):
    """Read at most MAX_DRAIN values, so the loop terminates."""
    total = 0
    for _ in range(MAX_DRAIN):
        total += queue.get()
    return total
