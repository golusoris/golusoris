package clock_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/clock"
)

func TestRealClockNow(t *testing.T) {
	t.Parallel()
	c := clockwork.NewRealClock()
	if c.Now().IsZero() {
		t.Error("real clock returned zero time")
	}
}

func TestFakeClockAdvance(t *testing.T) {
	t.Parallel()
	fc := clock.NewFake()
	start := fc.Now()
	fc.Advance(2 * time.Hour)
	if fc.Now().Sub(start) != 2*time.Hour {
		t.Error("fake clock did not advance correctly")
	}
}

// ExampleNewFake shows how to inject a controllable clock in tests.
func ExampleNewFake() {
	fc := clock.NewFake()
	t1 := fc.Now()
	fc.Advance(1 * time.Minute)
	fmt.Println(fc.Now().Sub(t1))
	// Output: 1m0s
}
