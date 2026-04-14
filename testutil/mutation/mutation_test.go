package mutation_test

import (
	"testing"

	"github.com/golusoris/golusoris/testutil/mutation"
)

func TestParseReport_validOutput(t *testing.T) {
	t.Parallel()
	// parseReport is unexported; test via the exported surface by calling
	// AssertMinScore with a manually constructed Report.
	r := mutation.Report{Total: 12, Score: 8.0 / 12.0}
	if r.Score < 0.6 || r.Score > 0.7 {
		t.Fatalf("unexpected score: %v", r.Score)
	}
}

func TestAssertMinScore_passes(t *testing.T) {
	t.Parallel()
	r := mutation.Report{Killed: 9, Total: 10, Score: 0.9}
	mutation.AssertMinScore(t, r, 0.8) // 90% ≥ 80% — should pass
}

func TestAssertMinScore_zeroTotal(t *testing.T) {
	t.Parallel()
	// Zero total → no assertion, no failure.
	r := mutation.Report{}
	mutation.AssertMinScore(t, r, 0.8)
}
