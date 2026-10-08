<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — pubsub/kafka/

fx-wired Kafka producer/consumer via twmb/franz-go v1.22.0.

## fx wiring

```go
fx.New(kafka.Module) // reads "kafka.*" from koanf config
```

Config keys (prefix `kafka`):

| Key | Default | Purpose |
| --- | --- | --- |
| `brokers` | `["localhost:9092"]` | Seed broker list |
| `group` | `""` | Consumer group ID (omit for producer-only) |
| `tls` | `false` | Enable TLS 1.2+ with system CA pool |
| `ca` | `""` | PEM CA bundle verifying brokers; implies TLS |
| `sasl.mechanism` | `""` | `PLAIN`, `SCRAM-SHA-256`, `SCRAM-SHA-512`; empty disables SASL |
| `sasl.user` | `""` | SASL user; required with mechanism |
| `sasl.password` / `sasl.passwordfile` | `""` | Exactly one; file read once, trailing newline trimmed |

Leaf keys stay one word: env mapping splits every underscore
(`APP_KAFKA_SASL_PASSWORDFILE` -> `kafka.sasl.passwordfile`). Credentials
without mechanism fail (`ErrSASLCredentials`); unknown mechanism fails
(`ErrUnsupportedSASLMechanism`). PLAIN without TLS logs warning.

## CloudEvents

```go
rec, err := kafka.NewCloudEventRecord("events", key, ev, cloudevents.ModeBinary)
err = client.Produce(ctx, rec)
ev, err := kafka.DecodeCloudEventRecord(rec)
```

- Binary mode: `ce_<attr>` headers, `datacontenttype` -> `content-type`,
 data -> record value (binding v1.0.2). Structured mode:
 `content-type: application/cloudevents+json; charset=UTF-8`.
- Decode: `content-type` prefix `application/cloudevents` -> structured;
 anything else -> binary. Header names case-insensitive; repeated attribute
 header -> `cloudevents.ErrMalformedEvent`. Missing `type`/`source` ->
 `*cloudevents.AttributeError` naming it.
- Key stays caller-provided. Binary event without data -> nil value
 (tombstone on compacted topics).

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

## Testing

Integration tests live in `pubsub/kafka/integration_test.go` and
`cloudevents_integration_test.go`; they require Docker (testutil/kafka
contract). `testutil/kafka.Addr(t)` spins Redpanda per test;
`AddrSASL(t, user, pass)` spins SCRAM-SHA-256-only Redpanda
(`TestIntegration_SASLSCRAM`: right password pings, wrong password fails start).

For tests that need `*kafka.Client` without full fx stack:

```go
import (
    "github.com/twmb/franz-go/pkg/kgo"
    "github.com/golusoris/golusoris/pubsub/kafka"
    kafkatest "github.com/golusoris/golusoris/testutil/kafka"
)

func TestSomething(t *testing.T) {
    addr := kafkatest.Addr(t)
    kc, _ := kgo.NewClient(kgo.SeedBrokers(addr))
    t.Cleanup(kc.Close)
    client := kafka.ClientFromKgo(kc)
    // ...
}
```

## Don't

- Don't call `Poll` before `Subscribe` — it will block indefinitely.
- Don't share single `Client` between producers and consumers in different
 goroutines without understanding franz-go's thread-safety guarantees (it is
 safe, but partition assignment may interfere).
- Don't use `time.Now()` in record timestamps — `NewRecord` does it correctly.
