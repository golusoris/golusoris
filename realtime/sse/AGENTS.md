# Agent guide — realtime/sse/

Server-Sent Events hub. Each connected HTTP client gets its own channel;
events published to the hub are broadcast to all clients.

## Usage

```go
hub := sse.NewHub(logger)

// Mount:
mux.Handle("/events", hub.Handler())

// Publish from a handler or background goroutine:
hub.Publish(ctx, sse.Event{
    Event: "order.updated",
    Data:  orderPayload, // struct → JSON, string → raw
})
```

## Event wire format

```
event: order.updated
data: {"id":"O-42","status":"shipped"}

```

## Don't

- Don't publish blocking work inside a handler — the Publish call holds
  an RLock for the duration of channel sends.
- Don't rely on SSE for reliable delivery — clients that disconnect
  miss events. For guaranteed delivery use webhooks or a job queue.
- Don't put auth inside the SSE handler — authenticate before upgrading
  the connection (middleware on the route).
