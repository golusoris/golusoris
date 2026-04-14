package mutation

import "testing"

func TestParseReport_valid(t *testing.T) {
	t.Parallel()
	r := parseReport("The mutation score is 0.666667 (8 of 12 mutations were killed)")
	if r.Score < 0.666 || r.Score > 0.667 {
		t.Errorf("Score = %v", r.Score)
	}
	if r.Killed != 8 {
		t.Errorf("Killed = %d, want 8", r.Killed)
	}
	if r.Total != 12 {
		t.Errorf("Total = %d, want 12", r.Total)
	}
}

func TestParseReport_noMatch(t *testing.T) {
	t.Parallel()
	r := parseReport("no results here")
	if r.Total != 0 || r.Killed != 0 || r.Score != 0 {
		t.Errorf("expected zero Report, got %+v", r)
	}
}

func TestParseReport_perfectScore(t *testing.T) {
	t.Parallel()
	r := parseReport("The mutation score is 1.000000 (10 of 10 mutations were killed)")
	if r.Total != 10 || r.Killed != 10 {
		t.Errorf("unexpected Report: %+v", r)
	}
}
