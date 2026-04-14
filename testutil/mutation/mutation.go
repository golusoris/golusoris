// Package mutation provides helpers for mutation testing via go-mutesting.
//
// go-mutesting must be installed separately:
//
//	go install github.com/avito-tech/go-mutesting/cmd/go-mutesting@latest
//
// If the binary is not found in PATH the test is automatically skipped.
//
// Usage:
//
//	func TestMutationScore(t *testing.T) {
//	    r := mutation.Run(t, "github.com/example/myapp/parser")
//	    mutation.AssertMinScore(t, r, 0.80) // require ≥80% mutation score
//	}
package mutation

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"testing"
)

// Report holds the parsed results of a go-mutesting run.
type Report struct {
	// Killed is the number of mutants caught by the test suite.
	Killed int
	// Total is the total number of generated mutants.
	Total int
	// Score is Killed/Total in [0,1]. Zero when Total == 0.
	Score float64
}

// Run executes go-mutesting against pkg and returns a parsed Report.
// The test is skipped when go-mutesting is not found in PATH.
// A non-zero exit code from go-mutesting (normal when mutants survive) is
// treated as a soft signal; parse the output regardless.
func Run(ctx context.Context, t *testing.T, pkg string) Report {
	t.Helper()
	binPath, err := exec.LookPath("go-mutesting")
	if err != nil {
		t.Skip("go-mutesting not found in PATH; " +
			"install: go install github.com/avito-tech/go-mutesting/cmd/go-mutesting@latest")
	}
	cmd := exec.CommandContext(ctx, binPath, pkg) //nolint:gosec // G204: pkg is a package path from trusted test code
	out, _ := cmd.CombinedOutput()               // non-zero exit expected when mutants survive
	t.Logf("go-mutesting output:\n%s", out)
	return parseReport(string(out))
}

// RunFiles executes go-mutesting on explicit source files instead of a
// package path. Use when the package is not importable.
func RunFiles(ctx context.Context, t *testing.T, files ...string) Report {
	t.Helper()
	binPath, err := exec.LookPath("go-mutesting")
	if err != nil {
		t.Skip("go-mutesting not found in PATH; " +
			"install: go install github.com/avito-tech/go-mutesting/cmd/go-mutesting@latest")
	}
	args := append([]string{"--"}, files...)          //nolint:gocritic // appendAssign: not a bug; files not reused
	cmd := exec.CommandContext(ctx, binPath, args...) //nolint:gosec // G204: files are from trusted test code
	out, _ := cmd.CombinedOutput()
	t.Logf("go-mutesting output:\n%s", out)
	return parseReport(string(out))
}

// AssertMinScore fails the test if r.Score is below min (fraction in [0,1]).
// When no mutants were generated the assertion is skipped with a log message.
func AssertMinScore(t *testing.T, r Report, min float64) {
	t.Helper()
	if r.Total == 0 {
		t.Log("mutation: no mutants generated — nothing to assert")
		return
	}
	if r.Score < min {
		t.Errorf("mutation score %.1f%% (killed %d/%d) is below minimum %.1f%%",
			r.Score*100, r.Killed, r.Total, min*100)
	}
}

// scoreRe matches go-mutesting's summary line, e.g.:
// "The mutation score is 0.666667 (8 of 12 mutations were killed)".
var scoreRe = regexp.MustCompile(`The mutation score is ([\d.]+) \((\d+) of (\d+) `)

func parseReport(out string) Report {
	m := scoreRe.FindStringSubmatch(out)
	if m == nil {
		return Report{}
	}
	score, _ := strconv.ParseFloat(m[1], 64)
	killed, _ := strconv.Atoi(m[2])
	total, _ := strconv.Atoi(m[3])
	return Report{Score: score, Killed: killed, Total: total}
}
