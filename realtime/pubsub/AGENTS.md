# Agent guide — realtime/pubsub/

In-process pub/sub bus (`LocalBus`). For cross-replica use, implement
`pubsub.Bus` backed by Redis SUBSCRIBE or Postgres LISTEN/NOTIFY.

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

Both `LocalBus` and future Redis/Postgres backends implement:

```go
type Bus interface {
    Publish(ctx context.Context, msg Message)
    Subscribe(topic string, h Handler) (cancel func())
}
```

Wire the interface into fx so backends are swappable without changing
subscribers.

## Don't

- Don't block in a Handler — it blocks the Publish caller.
- Don't use `LocalBus` in multi-replica deployments — events won't
  cross replica boundaries. Use `cache/redis` pubsub or Postgres
  LISTEN/NOTIFY instead.
