# Agent guide — testutil/redis/

Spins a real Redis container via testcontainers-go and returns a connected
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

For tests that drive their own client constructor (e.g. `cache/redis`), use
`redistest.Addr(t)` to get the container's `host:port` address instead of a
ready-made client.

## Don't

- Don't share a container across `t.Parallel` tests — call `Start` per test for isolation.
- Don't use in unit tests that don't need real Redis — mock the interface instead.
