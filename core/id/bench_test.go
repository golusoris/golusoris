// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package id_test

import (
	"testing"

	"github.com/golusoris/golusoris/core/id"
)

func BenchmarkNewUUID(b *testing.B) {
	g := id.New()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := g.NewUUID(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNewKSUID(b *testing.B) {
	g := id.New()
	b.ReportAllocs()
	for b.Loop() {
		_ = g.NewKSUID()
	}
}
