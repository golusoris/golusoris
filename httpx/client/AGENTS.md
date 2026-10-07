<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# httpx/client

Bounded outbound client. Retry. Breaker. OTel. Slog.

## Stack

```text
circuit-breaker → retry → otelhttp → stdlib transport
```

- Breaker outside retry. Open breaker stops request.
- Retry on network error, 429, 5xx.
- Default retry only safe/idempotent method.
- Unsafe method retry only with `Idempotency-Key` or `Retry.AllowUnsafe`.
- Retried body requires `Request.GetBody`. missing replay factory returns
 `ErrBodyNotReplayable` before network I/O; transport never buffers body.
- Exhausted HTTP-status retries return final response. caller owns body.
- OTel span per attempt.

## Rules

- One client per upstream. Unique `Name`.
- Non-positive timeout defaults to 30s. Caller context may be shorter.
- `CloneBounded(client, fallback)` clones injected clients and fills missing or
 non-positive timeouts without mutating caller-owned configuration.
- `ReadAllBounded(reader, max)` accepts exact-size bodies and returns
 `ErrBodyTooLarge` after one sentinel byte on overflow.
- Non-positive retry waits default to 500ms/10s; breaker open time to 30s.
- `Retry.Max=0`: retry off.
- `Breaker.Max=0`: breaker off.
- Use `Drain(ctx, resp)` on early exit. Drain caps at 1 MiB, five seconds, and
  two cleanup goroutines even when a custom body blocks in `Read` or `Close`.

## Never

- No `http.DefaultClient` for external I/O.
- No unsafe retry without idempotency proof.
- No large retry count. Retry amplifies outage load.
