<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — testutil/kafka/

Testcontainers helper that boots Redpanda broker (Kafka-API-compatible) for
integration tests. Follows same contract as `testutil/pg` and `testutil/redis`.

## API

```go
import kafkatest "github.com/golusoris/golusoris/testutil/kafka"

func TestMyHandler(t *testing.T) {
    addr := kafkatest.Addr(t)
    // addr is "host:port" — pass to kgo.SeedBrokers or kafka.Config.Brokers
}

addr := kafkatest.AddrSASL(t, "svc", "s3cret") // SCRAM-SHA-256 required; user is superuser
```

## Contract

- Requires Docker. `testcontainers.SkipIfProviderIsNotHealthy` skips cleanly
 on machines without reachable daemon instead of failing.
- Each `Addr` call starts fresh container; tests are isolated by default.
- Container is terminated via `t.Cleanup` — no manual teardown needed.
- Uses immutable Redpanda + Ryuk references from `internal/testimages`.
- Renovate owns tag + digest updates; no local mutable image strings.
- Redpanda runs `dev-container` mode: single node; no ZooKeeper.

## Don't

- Don't share single `Addr` call across parallel tests without coordination
 — each test should call `Addr(t)` independently for isolation.
- Don't change Redpanda advertise address without also updating port
 mapping; `localhost` must match what mapped port resolves to.
