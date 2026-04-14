# jonboulle/clockwork — v0.5.0 snapshot

Pinned: **v0.5.0**
Source: https://pkg.go.dev/github.com/jonboulle/clockwork@v0.5.0

## Interface

```go
type Clock interface {
    Now() time.Time
    Since(t time.Time) time.Duration
    Until(t time.Time) time.Duration
    Sleep(d time.Duration)
    NewTicker(d time.Duration) *time.Ticker
    NewTimer(d time.Duration) *time.Timer
    AfterFunc(d time.Duration, f func()) *time.Timer
}
```

## Real clock

```go
clk := clockwork.NewRealClock()
now := clk.Now()   // calls time.Now() internally
```

## Fake clock (tests)

```go
fc := clockwork.NewFakeClock()           // starts at 2015-01-01 00:00:00 UTC
fc := clockwork.NewFakeClockAt(t)        // starts at specific time

now := fc.Now()
fc.Advance(5 * time.Minute)             // advance time
fc.BlockUntil(1)                        // wait until 1 goroutine is sleeping
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
fc := clockwork.NewFakeClock()
svc := NewService(fc)
fc.Advance(time.Hour)
```

## golusoris usage

- `clock/` — `clockwork.Clock` provided via fx (real in prod, fake in tests via `fxtest`).
- `time.Now()` is **banned** outside `clock/` — use `clk.Now()` everywhere.

## Links

- Changelog: https://github.com/jonboulle/clockwork/blob/master/CHANGELOG.md
