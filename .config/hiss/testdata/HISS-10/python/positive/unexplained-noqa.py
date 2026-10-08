# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""An unexplained Ruff suppression rejected by the warning gate."""

import json  # noqa: F401


def describe(name):
    """Return a label while the suppressed import stays unused."""
    return f"name={name}"
