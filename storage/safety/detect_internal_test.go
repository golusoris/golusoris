// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package safety

import (
	"testing"

	"github.com/h2non/filetype/types"
)

// TestCategoryOf_UnregisteredType exercises categoryOf's default branch
// directly, from inside the package. Through the exported API (DetectBytes),
// every kind reaching categoryOf comes from filetype.Match, which — per
// h2non/filetype v1.1.3's matchers.init — only ever returns types.Unknown or
// a kind registered in one of the seven family maps categoryOf checks, so
// that default branch is unreachable that way. It is still real defensive
// code, not dead code: filetype.AddMatcher lets any importer of the shared
// filetype package register a matcher for a kind belonging to none of those
// seven maps. This test builds exactly such a kind and proves categoryOf
// degrades it to CategoryUnknown instead of panicking or misclassifying it,
// keeping the branch honestly covered per HISS-15 rather than left dead.
func TestCategoryOf_UnregisteredType(t *testing.T) {
	t.Parallel()
	kind := types.NewType("safety-internal-test-unregistered", "application/x-safety-internal-test")
	if got := categoryOf(kind); got != CategoryUnknown {
		t.Fatalf("categoryOf(unregistered) = %q, want %q", got, CategoryUnknown)
	}
}
