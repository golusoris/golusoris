package singleflight_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/golusoris/golusoris/cache/singleflight"
)

func TestDoDeduplicates(t *testing.T) {
	t.Parallel()
	g := singleflight.New[string, int]()

	var calls atomic.Int32
	var wg sync.WaitGroup
	const n = 10
	results := make([]int, n)
	errs := make([]error, n)

	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], _, errs[i] = g.Do(context.Background(), "key", func(_ context.Context) (int, error) {
				calls.Add(1)
				return 42, nil
			})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("[%d] unexpected error: %v", i, err)
		}
		if results[i] != 42 {
			t.Errorf("[%d] got %d, want 42", i, results[i])
		}
	}
	if c := calls.Load(); c > int32(n) {
		t.Errorf("fn called %d times, expected ≤ %d", c, n)
	}
}

func TestForgetAllowsNewCall(t *testing.T) {
	t.Parallel()
	g := singleflight.New[string, int]()
	ctx := context.Background()

	var calls atomic.Int32
	fn := func(_ context.Context) (int, error) {
		calls.Add(1)
		return int(calls.Load()), nil
	}

	v1, _, _ := g.Do(ctx, "k", fn)
	g.Forget("k")
	v2, _, _ := g.Do(ctx, "k", fn)

	if v1 == v2 {
		t.Errorf("expected different values after Forget, got %d and %d", v1, v2)
	}
}
