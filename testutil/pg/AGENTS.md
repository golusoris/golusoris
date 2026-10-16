<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — testutil/pg

PostgreSQL testcontainers harness. Real database; Docker mandatory; no mock fallback.

## Contract

- `Start(t)` -> connected `*pgxpool.Pool`. Container + pool teardown via `t.Cleanup`. Fresh container per call; tests isolated.
- `DSN(t)` -> raw connection string (for `db/migrate`).
- `StartReplication(t)` -> `(*pgxpool.Pool, replicationDSN)` with `wal_level=logical`.
- `StartTimescale(t)` -> `*pgxpool.Pool` with `timescaledb` extension enabled.
- PostgreSQL + TimescaleDB references: immutable `internal/testimages` authority.
- `Options.Image`: digest-pinned `tag@sha256:<64 lowercase hex>` required.
- Startup bounded by `startTimeout` (3 min scalar constant, HISS-02).
- Ryuk: immutable repository pin; `internal/testimages.WithPinnedReaper()`.
- Concurrency: `testutil/internal/startgate` limits parallel boots to 2 per test binary.

## Template cloning

One shared container per test binary via `TestMain`:

```go
func TestMain(m *testing.M) {
    os.Exit(pg.Main(m, func(ctx context.Context, dsn string) error {
        // apply migrations to template once
        return nil
    }))
}

func TestExample(t *testing.T) {
    pool := pg.Clone(t) // isolated cloned database
    // ...
}
```

- `Main(m, migrate, opts...)`: boots shared container, runs migration once against template database, terminates at exit.
- `Clone(t)`: creates fresh database via `CREATE DATABASE ... TEMPLATE template`, returns connected `*pgxpool.Pool`. Drops clone via `t.Cleanup`.
- `CloneDSN(t)`: same cloning lifecycle; returns connection string.

## Restrictions

- Never share containers across test files via package-level vars; use `Main` + `Clone` to amortize startup cost.
- Never call `t.Skip()` when Docker missing; CI without Docker signals defect.
