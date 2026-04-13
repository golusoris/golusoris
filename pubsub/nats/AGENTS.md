# Agent guide — pubsub/nats/

fx-wired NATS JetStream client via nats-io/nats.go v1.50.0.

## fx wiring

```go
fx.New(nats.Module) // reads "nats.*" from koanf config
```

Config keys (prefix `nats`):

| Key | Default | Purpose |
|---|---|---|
| `url` | `nats://localhost:4222` | Server URL |
| `name` | `""` | Client name shown in NATS monitoring |

## Core NATS (fire-and-forget)

```go
err := client.Publish("events.orders.created", payload)

sub, err := client.Subscribe("events.orders.*", func(msg *nats.Msg) {
    process(msg.Data)
})
defer sub.Unsubscribe()
```

## JetStream (durable, at-least-once)

```go
js := client.JetStream()

// Create stream:
_, err := js.CreateStream(ctx, jetstream.StreamConfig{
    Name:     "ORDERS",
    Subjects: []string{"orders.*"},
})

// Publish durably:
_, err = js.Publish(ctx, "orders.created", payload)

// Consume:
cons, err := js.CreateOrUpdateConsumer(ctx, "ORDERS", jetstream.ConsumerConfig{
    Durable: "my-service",
})
iter, err := cons.Messages()
for {
    msg, err := iter.Next()
    if err != nil { break }
    process(msg.Data())
    _ = msg.Ack()
}
```

## Don't

- Don't use core `Publish` for work that must survive server restarts — use JetStream.
- Don't call `client.Conn()` to publish from multiple goroutines concurrently
  without understanding NATS connection thread-safety (it is safe, but
  callbacks run on a single dispatch goroutine).
