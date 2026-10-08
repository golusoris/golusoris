<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — pubsub/nats/

fx-wired NATS JetStream client via nats-io/nats.go v1.54.0.

## fx wiring

```go
fx.New(nats.Module) // reads "nats.*" from koanf config
```

Config keys (prefix `nats`):

| Key | Default | Purpose |
| --- | --- | --- |
| `url` | `nats://localhost:4222` | Server URL |
| `name` | `""` | Client name shown in NATS monitoring |
| `creds` | `""` | JWT + NKey `.creds` file; re-read each connect |
| `nkey` | `""` | NKey user seed file; excludes `creds` |
| `tls.ca` / `tls.cert` / `tls.key` | `""` | PEM files; any set file enables TLS 1.2+ |
| `acktimeout` | `5s` | JetStream PubAck wait in `PublishCloudEvent` |

Leaf keys stay one word: env mapping splits every underscore
(`APP_NATS_TLS_CA` -> `nats.tls.ca`).

Reloading TLS: supply `*tls.Config` via
`nats.ProvideTLSConfig(func(...) *tls.Config {...})` (fx name
`golusoris.nats.tls`). Config is cloned. Combining it with `tls.*` files fails
with `ErrConflictingTLS`; `creds` plus `nkey` fails with `ErrConflictingAuth`.

## CloudEvents

```go
ack, err := client.PublishCloudEvent(ctx, "events.jobs", ev, cloudevents.ModeBinary)
msg, err := nats.NewCloudEventMsg(subject, ev, cloudevents.ModeStructured)
ev, err := nats.DecodeCloudEvent(msg.Headers(), msg.Data()) // jetstream.Msg or *nats.Msg
```

- `PublishCloudEvent`: JetStream publish, `Nats-Msg-Id` = `ev.ID`, waits for
 PubAck within `acktimeout` or ctx deadline. Same id twice inside stream
 `Duplicates` window -> stored once, `ack.Duplicate` true. Subject needs
 bound stream; otherwise `jetstream.ErrNoStreamResponse`.
- Binary mode: `ce-<attr>` headers, percent-encoded (binding 1.0.3-wip).
 Structured mode: `Content-Type: application/cloudevents+json`.
- Decode: header names case-insensitive. No `Content-Type` and no `ce-`
 header -> payload read as structured JSON (binding 1.0.2 producers).
 Missing `type`/`source` -> `*cloudevents.AttributeError` naming it.

## Readiness

Opt-in `nats.ReadinessModule` -> registers `ReadinessCheck(conn, timeout, logger)` (name `nats`) on app `*statuspage.Registry`. Up = status CONNECTED + flush round trip within 1s; RECONNECTING/CLOSED -> `ErrNotConnected`.

## Core NATS (fire-and-forget)

```go
err := client.Publish("events.orders.created", payload)
err = client.PublishSync(ctx, "events.orders.created", payload) // waits for server flush

sub, err := client.Subscribe("events.orders.*", func(msg *nats.Msg) {
    process(msg.Data)
})
defer sub.Unsubscribe()
```

`PublishSync` checks caller cancellation before publishing, then applies its
five-second flush deadline when context has none.

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

## Integration tests

`nats_test.go` contains testcontainers-backed tests (require Docker):

| Test | What it asserts |
| --- | --- |
| `TestIntegration_ConnectAndPing` | fx lifecycle connects; `Conn().IsConnected()` is true |
| `TestIntegration_PublishSubscribe` | core pub/sub delivers one message end-to-end |
| `TestIntegration_JetStreamAvailable` | `JetStream()` returns non-nil context |
| `TestIntegration_PublishCloudEventDedupes` | same event id twice -> one stored message per mode; decodes back |
| `TestIntegration_PublishCloudEventWithoutStreamFails` | no bound stream -> PubAck error, not silent success |

Use `testutil/nats.Start(t)` in downstream tests to spin fresh container.

## Don't

- Don't use core `Publish` for work that must survive server restarts — use JetStream.
- Use `PublishSync` before acknowledging external durable source. It confirms
 server receipt, not durable storage; use JetStream when persistence is required.
- Don't call `client.Conn()` to publish from multiple goroutines concurrently
 without understanding NATS connection thread-safety (it is safe, but
 callbacks run on single dispatch goroutine).
