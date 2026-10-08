// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package allocationfixture

import "testing"

var allocationBoundarySink []byte

// BenchmarkAllocationBudgetProbe meets the fixture budget of 8 B/op exactly.
func BenchmarkAllocationBudgetProbe(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		allocationBoundarySink = make([]byte, 8)
	}
}
