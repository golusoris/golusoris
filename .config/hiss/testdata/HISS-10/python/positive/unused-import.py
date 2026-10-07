# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""An unused import Ruff must report as F401."""

import json


def describe(name):
    """Return a label; the json import above is never used."""
    return "name=" + name
