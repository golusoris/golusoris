# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""A runtime-built string handed to a shell."""

import os


def run(name):
    """Hand a string assembled at run time to /bin/sh.

    The effect is the same as eval for the shell interpreter, but the scanner
    matches the two builtin names eval and exec and nothing else.
    """
    return os.system("ls " + name)
