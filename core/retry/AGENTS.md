<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — core/retry/

Shared retry primitive: capped exponential backoff + jitter, waits on injected `clock.Clock`, every wait ends on `ctx.Done()`. Stateless — no fx module. Deps: stdlib + `core/clock`.

## API

```go
err := retry.Do(ctx, func(ctx context.Context) error {
    return client.Ping(ctx)
}, retry.Policy{
    Initial: 100 * time.Millisecond, Max: 10 * time.Second,
    Multiplier: 2, Jitter: 0.2, MaxAttempts: 5,
    Retryable: func(err error) bool { return !errors.Is(err, errBadRequest) },
}, clk)
```

| Field | Rule |
| --- | --- |
| `MaxAttempts` | >= 1; counts first call |
| `Initial` | > 0; delay after first failure |
| `Max` | >= `Initial`; hard cap (jitter only shortens) |
| `Multiplier` | >= 1; growth saturates at `Max`, never overflows |
| `Jitter` | [0, 1]; delay drawn from `[d*(1-Jitter), d]` via `crypto/rand` |
| `Retryable` | nil = retry all errors |

`DefaultPolicy()` = 5 attempts, 100ms x2 -> 10s, 20% jitter.

## Errors

- `ErrInvalidPolicy` — bad policy, nil fn, nil clock; fn never called.
- `ErrExhausted` / `ErrPermanent` — wrapped together with fn error (`errors.Is` both).
- Done ctx — wraps `ctx.Err()` + last fn error; checked before every attempt.

## Tests

Fake clock: `BlockUntilContext(ctx, 1)` then `Advance(d - 1ns)` (no call) + `Advance(1ns)` (call) proves exact delays; bounded ctx turns wrong scheduling into failure, not hang.

## Don't

- Don't call `time.Sleep` / `time.After` around `Do` — pass clock.
- Don't loop over `Do` — raise `MaxAttempts`.
- Don't hide I/O deadline: bound fn work with caller ctx (HISS-02).
