<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — testutil/clickhouse/

Real ClickHouse testcontainer helper. Docker required. Fresh container per call.

## Contract

- ClickHouse + Ryuk references: immutable `internal/testimages` authority.
- Renovate owns tag + digest updates; no local mutable image strings.
- Startup: `testutil/internal/startgate` slot + three-minute scalar timeout.
- Cleanup: `t.Cleanup`; fresh non-cancelled termination context.
- Return: native address; caller owns application client.
