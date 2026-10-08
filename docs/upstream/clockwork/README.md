<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# jonboulle/clockwork — v0.5.0 snapshot

Pinned: **v0.5.0**
Source: [tagged source](https://github.com/jonboulle/clockwork/tree/v0.5.0)

## Interface

```go
type Clock interface {
    After(d time.Duration) <-chan time.Time
    Sleep(d time.Duration)
    Now() time.Time
    Since(t time.Time) time.Duration
    Until(t time.Time) time.Duration
    NewTicker(d time.Duration) Ticker
    NewTimer(d time.Duration) Timer
    AfterFunc(d time.Duration, f func()) Timer
}
```

## Real clock

```go
clk := clockwork.NewRealClock()
now := clk.Now()   // calls time.Now() internally
```

## Fake clock (tests)

```go
fc := clockwork.NewFakeClock()           // starts at current system time
fc := clockwork.NewFakeClockAt(t)        // deterministic start time

now := fc.Now()
fc.Advance(5 * time.Minute)             // advance time
if err := fc.BlockUntilContext(ctx, 1); err != nil {
    return fmt.Errorf("wait for clock consumer: %w", err)
}
```

## Usage pattern

```go
// Production
type Service struct { clk clockwork.Clock }

func NewService(clk clockwork.Clock) *Service { return &Service{clk: clk} }

func (s *Service) IsExpired(t time.Time) bool {
    return s.clk.Now().After(t)
}

// Test
fc := clockwork.NewFakeClockAt(time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC))
svc := NewService(fc)
fc.Advance(time.Hour)
```

## golusoris usage

- `core/clock/` — `clockwork.Clock` provided via fx (real in production, fake
  in tests via `fxtest`).
- `time.Now()` is banned outside `core/clock/`; use `clk.Now()` everywhere.

## Links

- [Package documentation](https://pkg.go.dev/github.com/jonboulle/clockwork@v0.5.0)
