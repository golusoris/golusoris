// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package allocationfixture

import "testing"

var allocationValueSink byte

// BenchmarkAllocationBudgetProbe stays below the fixture budget of 8 B/op.
func BenchmarkAllocationBudgetProbe(b *testing.B) {
	var values [8]byte
	b.ReportAllocs()
	for b.Loop() {
		allocationValueSink = values[0]
	}
}
