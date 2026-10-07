// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package p

import (
	"runtime"
	"testing"
)

// TestWindowsBehavior records the gap: go test exits zero when all platform work skips.
func TestWindowsBehavior(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows behavior has no implementation")
	}
}
