<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — realtime/pubsub/

In-process pub/sub bus (`LocalBus`). For cross-replica use,
`realtime/pubsub/redis` implements `pubsub.Bus` with Redis SUBSCRIBE;
Postgres LISTEN/NOTIFY remains extension point.

## Usage

```go
bus := pubsub.New()

// Subscribe:
cancel := bus.Subscribe("order.created", func(msg pubsub.Message) {
    // handle — must not block
    go process(msg)
})
defer cancel()

// Publish:
bus.Publish(ctx, pubsub.Message{Topic: "order.created", Data: order})
```

## Bus interface

`LocalBus` and `realtime/pubsub/redis.Bus` implement; future Postgres backend
uses same contract:

```go
type Bus interface {
    Publish(ctx context.Context, msg Message)
    Subscribe(topic string, h Handler) (cancel func())
}
```

Wire interface into fx so backends are swappable without changing
subscribers.

## Don't

- Don't block in Handler — it blocks Publish caller.
- Don't use `LocalBus` in multi-replica deployments — events won't
 cross replica boundaries. Use `realtime/pubsub/redis` or Postgres
 LISTEN/NOTIFY implementation instead.
