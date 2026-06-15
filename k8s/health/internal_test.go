package health

import (
	"testing"

	"github.com/golusoris/golusoris/observability/statuspage"
)

func TestServing_empty(t *testing.T) {
	t.Parallel()
	if !serving(nil) {
		t.Fatal("want true for nil (empty) results")
	}
}

func TestServing_allUp(t *testing.T) {
	t.Parallel()
	results := []statuspage.Result{
		{Status: statuspage.StatusUp},
		{Status: statuspage.StatusUp},
	}
	if !serving(results) {
		t.Fatal("want true when all checks are up")
	}
}

// TestServing_degradedStillServes is the #165 semantic: a degraded check must
// NOT fail the probe (still serving), but a down one must.
func TestServing_degradedStillServes(t *testing.T) {
	t.Parallel()
	if !serving([]statuspage.Result{{Status: statuspage.StatusUp}, {Status: statuspage.StatusDegraded}}) {
		t.Error("degraded check must still be serving (probe 200)")
	}
	if serving([]statuspage.Result{{Status: statuspage.StatusUp}, {Status: statuspage.StatusDown}}) {
		t.Error("a down check must fail the probe")
	}
	if serving([]statuspage.Result{{Status: statuspage.StatusUnknown}}) {
		t.Error("an unknown check must fail the probe")
	}
}

func TestOverall(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   []statuspage.Result
		want string
	}{
		{"all up", []statuspage.Result{{Status: statuspage.StatusUp}}, "up"},
		{"one degraded", []statuspage.Result{{Status: statuspage.StatusUp}, {Status: statuspage.StatusDegraded}}, "degraded"},
		{"down wins over degraded", []statuspage.Result{{Status: statuspage.StatusDegraded}, {Status: statuspage.StatusDown}}, "down"},
		{"unknown is down", []statuspage.Result{{Status: statuspage.StatusUnknown}}, "down"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := overall(c.in); got != c.want {
				t.Errorf("overall = %q, want %q", got, c.want)
			}
		})
	}
}
