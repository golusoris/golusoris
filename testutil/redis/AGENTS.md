<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — testutil/redis/

Spins real Redis container via testcontainers-go and returns connected
`rueidis.Client`. Docker required.

## Usage

```go
func TestMyWorker(t *testing.T) {
    c := redistest.Start(t)
    // c is a rueidis.Client — use normally
    cmd := c.B().Set().Key("x").Value("1").Build()
    _ = c.Do(ctx, cmd).Error()
}
```

Container + client are torn down via `t.Cleanup` — no manual cleanup needed.

Redis + Ryuk references: immutable `internal/testimages` authority. Renovate owns
tag + digest updates. No local mutable image strings.

For tests that drive their own client constructor (e.g. `cache/redis`), use
`redistest.Addr(t)` to get container's `host:port` address instead of ready-made client.

## Don't

- Don't share container across `t.Parallel` tests — call `Start` per test for isolation.
- Don't use in unit tests that don't need real Redis — mock interface instead.
