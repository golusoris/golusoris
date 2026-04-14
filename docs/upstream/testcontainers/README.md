# testcontainers/testcontainers-go — v0.37.0 snapshot

Pinned: **v0.37.0**
Source: https://pkg.go.dev/github.com/testcontainers/testcontainers-go@v0.37.0
Docs: https://golang.testcontainers.org

## PostgreSQL

```go
import (
    "github.com/testcontainers/testcontainers-go/modules/postgres"
    "github.com/testcontainers/testcontainers-go/wait"
)

pgContainer, err := postgres.RunContainer(ctx,
    testcontainers.WithImage("postgres:16-alpine"),
    postgres.WithDatabase("testdb"),
    postgres.WithUsername("test"),
    postgres.WithPassword("test"),
    testcontainers.WithWaitStrategy(
        wait.ForLog("database system is ready to accept connections").
            WithOccurrence(2).
            WithStartupTimeout(30*time.Second)),
)
defer pgContainer.Terminate(ctx)

connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
```

## Redis

```go
import "github.com/testcontainers/testcontainers-go/modules/redis"

redisContainer, err := redis.RunContainer(ctx,
    testcontainers.WithImage("redis:7-alpine"),
)
defer redisContainer.Terminate(ctx)

addr, err := redisContainer.ConnectionString(ctx)
```

## Generic container

```go
req := testcontainers.ContainerRequest{
    Image:        "my-image:latest",
    ExposedPorts: []string{"8080/tcp"},
    WaitingFor:   wait.ForHTTP("/health").WithPort("8080"),
    Env:          map[string]string{"ENV": "test"},
}
container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
    ContainerRequest: req,
    Started:          true,
})
defer container.Terminate(ctx)

host, _ := container.Host(ctx)
port, _ := container.MappedPort(ctx, "8080")
```

## Reuse pattern (speed up test suites)

```go
req := testcontainers.ContainerRequest{
    Image: "postgres:16-alpine",
    Reuse: true,   // reuse existing container with same name
    Name:  "test-pg",
}
```

## golusoris usage

- `testutil/pg/` — `Start(t)` returns pool; hard-fails when Docker unavailable (no Skip).
- `testutil/redis/` — `Start(t)` returns rueidis client.

## Links

- Changelog: https://github.com/testcontainers/testcontainers-go/blob/main/CHANGELOG.md
