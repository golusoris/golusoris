<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — idempotency/internal/storetest/

Shared test fixtures; store conformance + umbrella middleware replay integration.

- `Harness`: isolated store + clock advance callback.
- `Run`: positive, invalid-input, expiry-boundary, concurrency and ownership cases.
- `RunSweep`: bounded expiry cleanup. `SharedAcrossReplicas`: HTTP replay across stores.
- `OpenSQLite`: file-backed database fixture, reused by fx module tests.
- Test-only imports; never import from production backend code.
