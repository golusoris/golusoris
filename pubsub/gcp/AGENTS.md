<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — pubsub/gcp/

fx-wired Google Cloud Pub/Sub client via `cloud.google.com/go/pubsub/v2`.

## fx wiring

```go
fx.New(gcp.Module) // reads "pubsub.gcp.*" from koanf config
```

Config keys (prefix `pubsub.gcp`):

| Key | Default | Purpose |
|---|---|---|
| `project_id` | (required) | GCP project ID |

Authentication uses Application Default Credentials — no credential fields
in `Config`. Set `GOOGLE_APPLICATION_CREDENTIALS` (or run on GCP compute
with an attached service account) the same way any other `cloud.google.com/go`
client expects.

## Publishing

```go
id, err := client.Publish(ctx, "orders", orderJSON, map[string]string{"kind": "order"})
```

`Publish` caches one `*pubsub.Publisher` per topic (see `Client.Publisher`) so
repeated calls don't leak publishers; `Client.Close` stops every cached one.

## Subscribing

```go
err := client.Subscribe(ctx, "orders-sub", func(ctx context.Context, m *gcp.Message) {
    process(m.Data)
    m.Ack() // or m.Nack() to force a faster redelivery
})
```

`Subscribe` blocks until `ctx` is cancelled or an unrecoverable error occurs,
same shape as `pubsub.Subscriber.Receive`. Every message must be `Ack`ed or
`Nack`ed exactly once.

## Health check

`Client.Ping` lists at most one topic in the configured project to verify
connectivity; the fx module calls it on `OnStart` so misconfiguration (bad
project ID, missing credentials) fails fast instead of surfacing on first
publish.

## Advanced

```go
pc := client.Pubsub() // underlying *pubsub.Client for topic/subscription admin, etc.
```

## Testing

Unit tests in `gcp_test.go` run against an in-process
[`pstest`](https://pkg.go.dev/cloud.google.com/go/pubsub/v2/pstest) fake
server — no Docker, no real network, no GCP project required. For tests that
need a `*gcp.Client` without the full fx stack:

```go
import (
    "google.golang.org/api/option"
    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"

    pubsub "cloud.google.com/go/pubsub/v2"
    "cloud.google.com/go/pubsub/v2/pstest"
    "github.com/golusoris/golusoris/pubsub/gcp"
)

func TestSomething(t *testing.T) {
    srv := pstest.NewServer()
    t.Cleanup(func() { _ = srv.Close() })
    conn, _ := grpc.NewClient(srv.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
    t.Cleanup(func() { _ = conn.Close() })
    pc, _ := pubsub.NewClient(ctx, "test-project", option.WithGRPCConn(conn))
    client := gcp.ClientFromPubsub(pc)
    // ...
}
```

To target a real Pub/Sub emulator instead of `pstest`, set
`PUBSUB_EMULATOR_HOST` — the client library honors it automatically, no code
change needed.

## Don't

- Don't call `Subscribe` expecting it to return once a message arrives — it
  blocks like `pubsub.Subscriber.Receive` until `ctx` is done.
- Don't call `Stop` on a `*pubsub.Publisher` returned by `Client.Publisher` —
  the `Client` owns its lifecycle; call `Client.Close` instead.
- Don't add a config field for the Pub/Sub emulator endpoint — the SDK already
  honors `PUBSUB_EMULATOR_HOST`, and `pstest` tests dial the fake server
  directly via `option.WithGRPCConn` (see Testing above).
