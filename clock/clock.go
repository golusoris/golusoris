// Package clock exposes a mockable wall clock. All golusoris code that needs
// "now" or sleep should depend on [Clock] (provided by fx) instead of the
// stdlib time package directly. This makes time-sensitive logic testable.
//
//	fx.Invoke(func(c clock.Clock) {
//	    if time.Since(c.Now()) > timeout { ... }
//	})
//
// In tests, swap in [clockwork.NewFakeClock] via fx.Replace.
package clock

import (
	"github.com/jonboulle/clockwork"
	"go.uber.org/fx"
)

// Clock is the dependency apps inject. It's a re-export of clockwork.Clock so
// callers can use the rich clockwork API (Sleep, NewTicker, NewTimer, ...).
type Clock = clockwork.Clock

// Module provides a real wall clock. Tests can override with
// fx.Replace(clock.NewFake()).
var Module = fx.Module("golusoris.clock",
	fx.Provide(func() Clock { return clockwork.NewRealClock() }), //nolint:gocritic // explicit return type aids fx
)

// NewFake returns a controllable fake clock for tests. Sugar over clockwork.
func NewFake() *clockwork.FakeClock { return clockwork.NewFakeClock() }
