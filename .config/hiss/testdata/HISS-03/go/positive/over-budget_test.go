// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package allocationfixture

import "testing"

var allocationSink []byte

// BenchmarkAllocationBudgetProbe exceeds the fixture budget of 8 B/op.
func BenchmarkAllocationBudgetProbe(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		allocationSink = make([]byte, 16)
	}
}
