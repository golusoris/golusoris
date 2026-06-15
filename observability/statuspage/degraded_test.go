package statuspage_test

import (
	"context"
	"testing"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/observability/statuspage"
)

func TestDegradedResultAndDetails(t *testing.T) {
	t.Parallel()
	reg := statuspage.NewRegistry(clock.NewFake())
	reg.Register(statuspage.Check{
		Name: "cache",
		Fn:   func(context.Context) error { return statuspage.Degraded("disabled") },
		Details: func(context.Context) map[string]any {
			return map[string]any{"backend": "noop"}
		},
	})
	reg.Register(statuspage.Check{
		Name: "db",
		Fn:   func(context.Context) error { return nil },
	})

	results := reg.Run(context.Background())
	byName := map[string]statuspage.Result{}
	for _, r := range results {
		byName[r.Name] = r
	}

	if got := byName["cache"].Status; got != statuspage.StatusDegraded {
		t.Errorf("cache status = %q, want degraded", got)
	}
	if got := byName["cache"].Details["backend"]; got != "noop" {
		t.Errorf("cache details[backend] = %v, want noop", got)
	}
	if got := byName["db"].Status; got != statuspage.StatusUp {
		t.Errorf("db status = %q, want up", got)
	}
}
