# Agent guide — testutil/kafka/

Testcontainers helper that boots a Redpanda broker (Kafka-API-compatible) for
integration tests. Follows the same contract as `testutil/pg` and `testutil/redis`.

## API

```go
import kafkatest "github.com/golusoris/golusoris/testutil/kafka"

func TestMyHandler(t *testing.T) {
    addr := kafkatest.Addr(t)
    // addr is "host:port" — pass to kgo.SeedBrokers or kafka.Config.Brokers
}
```

## Contract

- Requires Docker. `testcontainers.SkipIfProviderIsNotHealthy` skips cleanly
  on machines without a reachable daemon instead of failing.
- Each `Addr` call starts a fresh container; tests are isolated by default.
- Container is terminated via `t.Cleanup` — no manual teardown needed.
- Uses Redpanda `v24.3.1` in `dev-container` mode (single node, no ZooKeeper).

## Don't

- Don't share a single `Addr` call across parallel tests without coordination
  — each test should call `Addr(t)` independently for isolation.
- Don't change the Redpanda advertise address without also updating the port
  mapping; `localhost` must match what the mapped port resolves to.
