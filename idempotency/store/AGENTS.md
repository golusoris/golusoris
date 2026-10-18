<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — idempotency/store/

Atomic contracts + process-local memory store. Standard library + clockwork; no middleware, fx, database clients or `core/validate`.

- `Store`: fingerprinted `Claim`, token-bound `Commit` / `Release`.
- `MemoryStore`: injected clock; defensive response copies; one process only.
- `Sweeper`: bounded expiry cleanup, `MaxSweepBatch` = 10000.
- Shared argument checks: `../internal/storeutil/`. Typed-nil clock -> real clock.
- Verification: `go test -race -count=1 ./idempotency/store`.
