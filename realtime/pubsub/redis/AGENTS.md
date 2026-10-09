<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — realtime/pubsub/redis

Cross-replica pub/sub `Bus` backed by Redis PUBLISH/SUBSCRIBE (rueidis).
Implements `realtime/pubsub.Bus` — drop-in replacement for `pubsub.LocalBus`
when messages must reach subscribers on other replicas.

## Wiring

```go
fx.New(golusoris.Core, golusoris.CacheRedis, pubsubredis.Module) // provides pubsub.Bus
```

`Module` provides `pubsub.Bus` from injected `rueidis.Client`.

## Wire format

`Message.Data` is encoded on publish: `[]byte` and `string` pass through, any
other value is JSON-marshalled. Subscribers receive `Data` as raw `[]byte`
payload — decode as needed.

## Semantics vs LocalBus

- `Subscribe` = `SubscribeWithGap(topic, h, nil)`. Background goroutine holds
  dedicated connection with `PubSubHooks`; returned func cancels, releases
  connection (rueidis unsubscribes on release).
- Connection drop -> resubscribe. `core/retry` backoff 100ms..30s, 20% jitter,
  4096 attempts per outage; `maxSubscribeSessions` (2^20) bounds reconnects.
  Works with `DisableRetry` clients too: `Receive` path retried only inside rueidis.
- Lifetime = stop channel, never deadline-less ctx (HISS-02). Outage ctx deadline:
  4096 x (30s cap + 10s SUBSCRIBE timeout); SUBSCRIBE round trip: 10s.
- `onGap` after every resubscribe, never after first subscribe. Runs on
  subscription goroutine; keep fast.
- `Publish` is fire-and-forget (Bus contract); transport errors are logged, not returned.
- Redis pub/sub is at-most-once + fan-out to currently-connected subscribers
 (no persistence/replay). For durable delivery use `pubsub/nats` JetStream or `jobs/`.

## Tests

`redis_test.go`: hermetic `encode` + policy bound tests; testcontainers
round-trip, connection drop via `CLIENT KILL TYPE pubsub` (with and without
rueidis retry, one `onGap`), cancel -> `PUBSUB NUMSUB` 0 (`testutil/redis`,
requires Docker).

## Don't

- Don't assume delivery guarantees — Redis pub/sub drops messages for offline subscribers.
- Don't send non-serialisable `Data` and expect cross-replica fidelity — it is JSON-encoded.
