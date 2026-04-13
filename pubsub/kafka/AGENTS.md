# Agent guide — pubsub/kafka/

fx-wired Kafka producer/consumer via twmb/franz-go v1.20.7.

## fx wiring

```go
fx.New(kafka.Module) // reads "kafka.*" from koanf config
```

Config keys (prefix `kafka`):

| Key | Default | Purpose |
|---|---|---|
| `brokers` | `["localhost:9092"]` | Seed broker list |
| `group` | `""` | Consumer group ID (omit for producer-only) |
| `tls` | `false` | Enable TLS with system CA pool |

## Producing

```go
err := client.Produce(ctx,
    kafka.NewRecord("orders", []byte("order-id"), orderJSON),
)
```

## Consuming

```go
client.Subscribe("orders", "payments") // set topics before polling
for {
    records, err := client.Poll(ctx, 100)
    if err != nil { ... }
    for _, r := range records {
        process(r)
    }
    _ = client.CommitOffsets(ctx)
}
```

## Advanced

```go
kc := client.Kgo() // underlying *kgo.Client for transactions, admin API, etc.
```

## Don't

- Don't call `Poll` before `Subscribe` — it will block indefinitely.
- Don't share a single `Client` between producers and consumers in different
  goroutines without understanding franz-go's thread-safety guarantees (it is
  safe, but partition assignment may interfere).
- Don't use `time.Now()` in record timestamps — `NewRecord` does it correctly.
