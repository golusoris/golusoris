# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""An exception handler that names exactly what it handles."""


def load(path):
    """Handle the one failure this caller can recover from, re-raise the rest."""
    try:
        with open(path, encoding="utf-8") as handle:
            return handle.read()
    except FileNotFoundError:
        return ""
