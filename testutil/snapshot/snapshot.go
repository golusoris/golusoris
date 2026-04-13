// Package snapshot provides snapshot testing helpers backed by
// gkampitakis/go-snaps.
//
// Snapshots are stored in __snapshots__/ next to the test file and committed
// to the repository. On first run (or after deletion) the snapshot is created.
// On subsequent runs the test output is compared to the stored snapshot.
//
// Update snapshots:
//
//	UPDATE_SNAPS=true go test ./...
//
// Usage:
//
//	func TestRender(t *testing.T) {
//	    got := render(input)
//	    snapshot.Match(t, got)
//	}
package snapshot

import (
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
)

// Match asserts that value matches the stored snapshot for the current test.
// On first run the snapshot is created. Set UPDATE_SNAPS=true to update.
func Match(t *testing.T, value any) {
	t.Helper()
	snaps.MatchSnapshot(t, value)
}

// MatchJSON asserts that the JSON-serialisable value matches the stored
// snapshot, formatted as indented JSON for readability.
func MatchJSON(t *testing.T, value any) {
	t.Helper()
	snaps.MatchJSON(t, value)
}
