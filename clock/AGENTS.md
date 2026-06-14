# Agent guide — clock/

Mockable wall clock. Any golusoris code that needs "now" or sleep depends on
[`Clock`](clock.go) (injected via fx) instead of the stdlib `time` package, so
time-sensitive logic is testable.

`Clock` is a re-export of [`clockwork.Clock`](https://github.com/jonboulle/clockwork)
— callers get the full clockwork API (`Now`, `Sleep`, `After`, `NewTicker`,
`NewTimer`, …).

## Key API

| Symbol | Purpose |
|---|---|
| `clock.Clock` | The injected dependency (alias for `clockwork.Clock`) |
| `clock.Module` | fx module — provides a real wall clock |
| `clock.NewFake()` | `*clockwork.FakeClock` for tests |

## Usage

```go
fx.New(golusoris.Core, clock.Module)

func NewExpiry(c clock.Clock) *Expiry {
    return &Expiry{deadline: c.Now().Add(timeout)}
}
```

In tests, advance time deterministically:

```go
fc := clock.NewFake()
app := fxtest.New(t, fx.Replace(clock.Clock(fc)), ...)
fc.Advance(2 * time.Hour) // fire timers/tickers without waiting
```

## Don't

- Don't call `time.Now()` / `time.Sleep()` directly anywhere outside this
  package — it defeats the testability contract and trips the project lint gate.
- Don't store the result of `c.Now()` and assume it stays current — re-read on
  each use.
- Don't `fx.Provide` a second `Clock` — `clock.Module` already provides one;
  override with `fx.Replace` in tests instead.
