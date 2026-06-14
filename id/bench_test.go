package id_test

import (
	"testing"

	"github.com/golusoris/golusoris/id"
)

func BenchmarkNewUUID(b *testing.B) {
	g := id.New()
	b.ReportAllocs()
	for b.Loop() {
		_ = g.NewUUID()
	}
}

func BenchmarkNewKSUID(b *testing.B) {
	g := id.New()
	b.ReportAllocs()
	for b.Loop() {
		_ = g.NewKSUID()
	}
}
