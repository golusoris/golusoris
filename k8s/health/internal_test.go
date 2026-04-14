package health

import (
	"testing"

	"github.com/golusoris/golusoris/observability/statuspage"
)

func TestAllUp_empty(t *testing.T) {
	t.Parallel()
	if !allUp(nil) {
		t.Fatal("want true for nil (empty) results")
	}
}

func TestAllUp_allUp(t *testing.T) {
	t.Parallel()
	results := []statuspage.Result{
		{Status: statuspage.StatusUp},
		{Status: statuspage.StatusUp},
	}
	if !allUp(results) {
		t.Fatal("want true when all checks are up")
	}
}

func TestAllUp_oneDown(t *testing.T) {
	t.Parallel()
	results := []statuspage.Result{
		{Status: statuspage.StatusUp},
		{Status: statuspage.StatusDown},
	}
	if allUp(results) {
		t.Fatal("want false when any check is not up")
	}
}

func TestStatusString_true(t *testing.T) {
	t.Parallel()
	if got := statusString(true); got != "up" {
		t.Fatalf("want %q, got %q", "up", got)
	}
}

func TestStatusString_false(t *testing.T) {
	t.Parallel()
	if got := statusString(false); got != "down" {
		t.Fatalf("want %q, got %q", "down", got)
	}
}
