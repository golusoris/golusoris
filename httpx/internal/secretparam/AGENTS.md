<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# httpx/internal/secretparam

- Shared defaults: httpx/client span redaction; httpx/httpcache policy.
- Name matching: lowercase; underscores and hyphens removed.
- Extra names extend defaults; returned defaults caller-owned.
- Stateless helpers; no I/O; no dependencies beyond stdlib.
