<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — realtime/sse/

Server-Sent Events hub. Each connected HTTP client gets its own channel;
events published to hub are broadcast to all clients.

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

A nil logger is accepted and uses a discard handler.

## Event wire format

```text
event: order.updated
data: {"id":"O-42","status":"shipped"}

```

## Wire rules

- `ID` and `Event`: reject CR, LF, NUL.
- String/byte data: CRLF and CR normalize to LF.
- Every data line gets own `data:` prefix. Blank line preserved.
- Structured data: JSON encode.

## Don't

- Don't publish blocking work inside handler — Publish call holds
 RLock for duration of channel sends.
- Don't rely on SSE for reliable delivery — clients that disconnect
 miss events. For guaranteed delivery use webhooks or job queue.
- Don't put auth inside SSE handler — authenticate before upgrading
 connection (middleware on route).
- Don't put untrusted bytes into `ID` or `Event` without handling publish error.
