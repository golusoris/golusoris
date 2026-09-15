# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""Runtime evaluation of a string as code."""


def compute(expression):
    """Evaluate a caller-supplied string, so the behaviour is not static."""
    return eval(expression)
