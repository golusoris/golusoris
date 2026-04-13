package memory_test

import (
	"testing"

	"github.com/golusoris/golusoris/cache/memory"
)

func TestTypedCacheSetGet(t *testing.T) {
	t.Parallel()
	c, err := memory.NewForTest(100, 0)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	tc := memory.Typed[string, int](c, "num")
	tc.Set("a", 42)

	v, ok := tc.Get("a")
	if !ok {
		t.Fatal("expected hit")
	}
	if v != 42 {
		t.Errorf("got %d, want 42", v)
	}
}

func TestTypedCacheDelete(t *testing.T) {
	t.Parallel()
	c, err := memory.NewForTest(100, 0)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	tc := memory.Typed[string, string](c, "s")
	tc.Set("k", "v")
	tc.Delete("k")

	if _, ok := tc.Get("k"); ok {
		t.Error("expected miss after delete")
	}
}

func TestTypedCacheMiss(t *testing.T) {
	t.Parallel()
	c, err := memory.NewForTest(100, 0)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	tc := memory.Typed[string, int](c, "x")
	if _, ok := tc.Get("missing"); ok {
		t.Error("expected miss")
	}
}

func TestPrefixIsolation(t *testing.T) {
	t.Parallel()
	c, err := memory.NewForTest(100, 0)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	a := memory.Typed[string, int](c, "ns-a")
	b := memory.Typed[string, int](c, "ns-b")
	a.Set("k", 1)
	b.Set("k", 2)

	va, _ := a.Get("k")
	vb, _ := b.Get("k")
	if va != 1 || vb != 2 {
		t.Errorf("prefix isolation broken: a=%d b=%d", va, vb)
	}
}
