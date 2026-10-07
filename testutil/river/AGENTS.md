<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — testutil/river

Boots real river client backed by Postgres testcontainer for
integration tests.

## Conventions

- `Start(t, Options{Register: ...})` returns Harness with Pool +
 Workers + Client. Tears down on t.Cleanup.
- Register workers via `opts.Register` — client starts only when
 workers are registered (insert-only otherwise).
- `Harness.WaitForJob(ctx, kind)` polls until job reaches terminal
 state. Use for deterministic integration tests (no sleep-based
 waits).

## Don't

- Don't re-run same harness across tests — fresh Postgres per test
 is isolation contract. If that's too slow for your suite, share
 single pool + truncate river tables between tests.
