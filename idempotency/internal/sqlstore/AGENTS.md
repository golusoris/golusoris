<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — idempotency/internal/sqlstore/

Shared SQL reservation / completion algorithm; no dialect clients.

- `RowBackend`: dialect operations implemented by postgres and sqlite.
- `Record` / `Completion`: internal dialect boundary; no application imports.
- Reserve / lookup retry cap = 4; token loss + fingerprint errors preserve sentinels.
- Clock injection; validate before backend I/O. No middleware / fx imports.
