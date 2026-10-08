<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — outbox/cdc/

CDC outbox drain. Watches Postgres WAL. Sends committed inserts to sinks.
Lower latency than polling.

## API

```go
fx.New(
    golusoris.Core, golusoris.DB,
    dbcdc.Module,
    cdc.Module,                       // provides *cdc.Drainer, installs handler
    cdc.ProvideSinkFn(func(kc *kafka.Client) cdc.Sink {
        return cdc.NewKafkaSink(kc, "events")
    }),
)
```

- Provides `*cdc.Drainer`.
- Requires `*dbcdc.Consumer`, `*pgxpool.Pool`, and at least one grouped sink.
- Module installs handler. It has no lifecycle hook.
- Config prefix: `outbox.cdc`.

```go
type Sink interface { Send(ctx context.Context, ev outbox.Event) error }
```

## CloudEvents sinks

```go
cdc.ProvideSinkFn(func(nc *nats.Client) cdc.Sink {
    return cdc.NewNATSCloudEventSink(nc, "events.jobs", cdc.CloudEventOptions{
        Source: "/vmafx/controller", Mode: cloudevents.ModeBinary,
    })
})
cdc.NewKafkaCloudEventSink(kc, "events", cdc.CloudEventOptions{Source: "/vmafx/controller"})
```

- Row -> `outbox.Event.CloudEvent(opts.Source)`; row source wins.
- NATS: JetStream publish, `Nats-Msg-Id` = row `event_id`, waits for PubAck.
 WAL replay inside stream `Duplicates` window -> stored once. Subject needs
 bound stream.
- Kafka: key = subject, else kind. No broker dedupe; consumers dedupe on
 `source` + `id`.
- `Timeout` bounds one Send (default 10s). Zero `Mode` = binary.
- Rows without `event_id` (pre-migration) fail with `*cloudevents.AttributeError`
 naming `id`; legacy JSON sinks still accept them.

## Notes

- Pick CDC or polling drainer per app. Do not wire both.
- Replicas sharing one slot form active/standby sessions through reconnect.
- Sink failure leaves commit unacknowledged. WAL replays after reconnect.
- Successful delivery marks source row dispatched before WAL acknowledgement.
- `NATSSink` flushes core-NATS output before source row is marked. Use
 `NATSCloudEventSink` (JetStream + PubAck) when broker persistence is required.
- Webhooks reject redirects before forwarding method, payload, or secret.
- Webhook clients are cloned and receive 10-second timeout when unset.
- Sink order is unspecified. A successful sink can see duplicates when another
 sink fails. All sinks must be idempotent.
- Malformed outbox row fails handler. Never acknowledge silent loss.
- Outbox decoding consumes canonical CDC column values and rejects NULL, binary, or unchanged required fields.
- Requires Postgres logical replication. See `db/cdc/AGENTS.md`.
