<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — testutil/internal/startgate/

**Internal** package, imported only by testutil container helpers
(`pg`, `redis`, `nats`, `kafka`, `clickhouse`). It bounds how many containers
one test binary boots at once (`Limit` = 2).

## Why

`go test -p` bounds how many *packages* run together, not `t.Parallel`
tests inside one. package whose parallel tests each boot container starts
them all at once — `ai/tiny` booted ten Postgres containers together, and on
CPU-capped CI runner none logged ready within testcontainers' 60 s wait.

## Use

Take slot immediately before helper's `startTimeout` context, so start budget only runs once slot is held:

```go
defer startgate.Acquire(t)()
ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
defer cancel()
```

wait for slot is bounded by `acquireTimeout` (10 min, per-package
`go test` timeout). `release` is idempotent. new container helper under
`testutil/` must take slot same way.
