<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — testutil/internal/startgate/

**Internal** package, imported only by the testutil container helpers
(`pg`, `redis`, `nats`, `kafka`, `clickhouse`). It bounds how many containers
one test binary boots at once (`Limit` = 2).

## Why

`go test -p` bounds how many *packages* run together, not the `t.Parallel`
tests inside one. A package whose parallel tests each boot a container starts
them all at once — `ai/tiny` booted ten Postgres containers together, and on
the CPU-capped CI runner none logged ready within testcontainers' 60 s wait.

## Use

Take the slot immediately before the helper's `startTimeout` context, so the
start budget only runs once a slot is held:

```go
defer startgate.Acquire(t)()
ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
defer cancel()
```

The wait for a slot is bounded by `acquireTimeout` (10 min, the per-package
`go test` timeout). `release` is idempotent. A new container helper under
`testutil/` must take a slot the same way.
