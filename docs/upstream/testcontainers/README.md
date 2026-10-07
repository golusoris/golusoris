<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# testcontainers/testcontainers-go — v0.44.0 snapshot

Pinned: **v0.44.0**
Source: [tagged source](https://github.com/testcontainers/testcontainers-go/tree/v0.44.0)
Docs: [Testcontainers for Go](https://golang.testcontainers.org)

## PostgreSQL

```go
import (
    "github.com/testcontainers/testcontainers-go/modules/postgres"
    "github.com/testcontainers/testcontainers-go/wait"
)

pgContainer, err := postgres.Run(ctx, postgresImage,
    postgres.WithDatabase("testdb"),
    postgres.WithUsername("test"),
    postgres.WithPassword("test"),
    testcontainers.WithWaitStrategy(
        wait.ForLog("database system is ready to accept connections").
            WithOccurrence(2).
            WithStartupTimeout(30*time.Second)),
)
testcontainers.CleanupContainer(t, pgContainer)
if err != nil {
    t.Fatalf("start PostgreSQL: %v", err)
}

connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
if err != nil {
    t.Fatalf("get PostgreSQL connection string: %v", err)
}
```

## Redis

```go
import "github.com/testcontainers/testcontainers-go/modules/redis"

redisContainer, err := redis.Run(ctx, redisImage)
testcontainers.CleanupContainer(t, redisContainer)
if err != nil {
    t.Fatalf("start Redis: %v", err)
}

addr, err := redisContainer.ConnectionString(ctx)
if err != nil {
    t.Fatalf("get Redis connection string: %v", err)
}
```

## Generic container

```go
container, err := testcontainers.Run(ctx, image,
    testcontainers.WithExposedPorts("8080/tcp"),
    testcontainers.WithWaitStrategy(wait.ForHTTP("/health").WithPort("8080/tcp")),
    testcontainers.WithEnv(map[string]string{"ENV": "test"}),
)
testcontainers.CleanupContainer(t, container)
if err != nil {
    t.Fatalf("start container: %v", err)
}

host, err := container.Host(ctx)
if err != nil {
    t.Fatalf("get container host: %v", err)
}
port, err := container.MappedPort(ctx, "8080/tcp")
if err != nil {
    t.Fatalf("get mapped port: %v", err)
}
```

## Reuse pattern (speed up test suites)

```go
container, err := testcontainers.Run(ctx, postgresImage,
    testcontainers.WithReuseByName("test-pg"),
)
testcontainers.CleanupContainer(t, container)
if err != nil {
    t.Fatalf("reuse PostgreSQL: %v", err)
}
```

`postgresImage`, `redisImage`, and `image` must be immutable digest-pinned
references owned by the test suite. Reuse is opt-in and requires a stable,
non-empty name.

## golusoris usage

- `testutil/pg/` — `Start(t)` returns a pool and fails when Docker is
  unavailable; it does not skip.
- `testutil/redis/` — `Start(t)` returns rueidis client.

## Links

- [Package documentation](https://pkg.go.dev/github.com/testcontainers/testcontainers-go@v0.44.0)
