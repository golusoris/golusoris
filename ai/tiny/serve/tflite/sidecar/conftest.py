# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2

"""Pytest configuration for LiteRT sidecar test suite."""

from __future__ import annotations

import sys
from pathlib import Path

# Add repository root to sys.path so "import ai.tiny..." works
REPO_ROOT = Path(__file__).resolve().parents[5]
if str(REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(REPO_ROOT))
