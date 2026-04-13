# Agent guide — webhooks/out/

Outbound webhook delivery with HMAC-SHA256 signing, exponential-backoff retry,
dead-letter queue, and replay.

## Core types

| Type | Purpose |
|---|---|
| `Endpoint` | Registered subscription: URL, secret, event filter, active flag |
| `Delivery` | Delivery record: attempts, status, last HTTP code, error |
| `Store` | Persistence interface — implement with Postgres, SQLite, or `memStore` for tests |
| `Dispatcher` | Signs + delivers; created via `New(store, opts, logger, clk)` |

## Usage

```go
d := out.New(store, out.Options{MaxAttempts: 5}, logger, clk)

// Register an endpoint:
_ = store.SaveEndpoint(ctx, out.Endpoint{
    ID: "ep1", URL: "https://example.com/wh",
    Secret: secret, Events: []string{"order.created"}, Active: true,
})

// Dispatch — runs synchronously; wrap in a goroutine or river job:
_ = d.Dispatch(ctx, "order.created", payload)

// Replay a dead-lettered delivery:
_ = d.Replay(ctx, deliveryID)
```

## Signing

Signs with `sha256=<hex>` in `SignHeader` (default `X-Webhook-Signature`),
matching `webhooks/in.HMAC`. Also sets `X-Webhook-Event` and `X-Webhook-Delivery`.

## Retry behaviour

- Default: 5 attempts with exponential backoff (1 s, 2 s, 4 s, 8 s, 5 min cap).
- Override via `Options.Backoff` (return 0 in tests to avoid sleeping).
- Dead-letter = `StatusFailed` delivery after all attempts exhausted.
- Replay resets `Attempts` to 0 and re-runs the full retry loop.

## Don't

- Don't call `Dispatch` on the hot path without a background queue — it blocks.
- Don't store raw secrets in `Endpoint.Secret` without encryption at rest.
