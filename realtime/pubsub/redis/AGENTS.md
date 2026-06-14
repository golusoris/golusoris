# Agent guide — realtime/pubsub/redis

Cross-replica pub/sub `Bus` backed by Redis PUBLISH/SUBSCRIBE (rueidis).
Implements `realtime/pubsub.Bus` — a drop-in replacement for `pubsub.LocalBus`
when messages must reach subscribers on other replicas.

## Wiring

```go
fx.New(golusoris.Core, golusoris.CacheRedis, pubsubredis.Module) // provides pubsub.Bus
```

`Module` provides `pubsub.Bus` from the injected `rueidis.Client`.

## Wire format

`Message.Data` is encoded on publish: `[]byte` and `string` pass through, any
other value is JSON-marshalled. Subscribers receive `Data` as the raw `[]byte`
payload — decode as needed.

## Semantics vs LocalBus

- `Subscribe` runs a background `Receive` goroutine; the returned func cancels it.
- `Publish` is fire-and-forget (Bus contract); transport errors are logged, not returned.
- Redis pub/sub is at-most-once + fan-out to currently-connected subscribers
  (no persistence/replay). For durable delivery use `pubsub/nats` JetStream or `jobs/`.

## Tests

`redis_test.go` has a hermetic `encode` test + a testcontainers round-trip
(`testutil/redis`, requires Docker).

## Don't

- Don't assume delivery guarantees — Redis pub/sub drops messages for offline subscribers.
- Don't send non-serialisable `Data` and expect cross-replica fidelity — it is JSON-encoded.
