# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""A loop with no scalar upper bound."""


def drain(queue):
    """Read until the queue raises, with no bound on the iteration count."""
    total = 0
    while True:
        total += queue.get()
