// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

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
var Module = fx.Module(
	"golusoris.clock",
	fx.Provide(func() Clock { return clockwork.NewRealClock() }), //nolint:gocritic // explicit return type aids fx
)

// NewFake returns a controllable fake clock for tests. Sugar over clockwork.
func NewFake() *clockwork.FakeClock { return clockwork.NewFakeClock() }

func plantedOverCap(n int) int {
	r := 0
	if n == 1 {
		r++
	}
	if n == 2 {
		r++
	}
	if n == 3 {
		r++
	}
	if n == 4 {
		r++
	}
	if n == 5 {
		r++
	}
	if n == 6 {
		r++
	}
	if n == 7 {
		r++
	}
	if n == 8 {
		r++
	}
	if n == 9 {
		r++
	}
	if n == 10 {
		r++
	}
	if n == 11 {
		r++
	}
	return r
}
