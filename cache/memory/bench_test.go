package memory_test

import (
	"testing"
	"time"

	"github.com/golusoris/golusoris/cache/memory"
)

// BenchmarkTypedSetGet measures a Set+Get round trip on the otter-backed L1
// cache (the read-through hot path).
func BenchmarkTypedSetGet(b *testing.B) {
	c, err := memory.NewForTest(10_000, time.Minute)
	if err != nil {
		b.Fatal(err)
	}
	tc := memory.Typed[string, int](c, "bench")
	b.ReportAllocs()
	for b.Loop() {
		tc.Set("k", 42)
		_, _ = tc.Get("k")
	}
}
