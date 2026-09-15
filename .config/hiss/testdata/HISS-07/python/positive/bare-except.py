# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""An exception handler that catches and suppresses everything."""


def load(path):
    """Swallow every exception, including the ones that must propagate."""
    try:
        with open(path, encoding="utf-8") as handle:
            return handle.read()
    except:
        return ""
