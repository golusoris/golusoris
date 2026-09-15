# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""The static equivalent of the positive fixture."""

OPERATIONS = {
    "add": lambda a, b: a + b,
    "sub": lambda a, b: a - b,
}


def compute(name, a, b):
    """Dispatch through a closed table, so every branch exists in the source."""
    return OPERATIONS[name](a, b)
