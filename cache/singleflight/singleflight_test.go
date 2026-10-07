// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package singleflight_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golusoris/golusoris/cache/singleflight"
)

type collidingKey struct{ id int }

func (collidingKey) String() string { return "same" }

type callResult struct {
	value int
	err   error
}

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
	var calls atomic.Int32
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstResult := make(chan callResult, 1)
	go func() {
		value, _, err := g.Do(context.Background(), "k", func(context.Context) (int, error) {
			calls.Add(1)
			close(firstStarted)
			<-releaseFirst
			return 1, nil
		})
		firstResult <- callResult{value: value, err: err}
	}()
	<-firstStarted
	g.Forget("k")
	second, _, err := g.Do(context.Background(), "k", func(context.Context) (int, error) {
		calls.Add(1)
		return 2, nil
	})
	if err != nil || second != 2 {
		t.Fatalf("second call = %d, %v", second, err)
	}
	close(releaseFirst)
	first := <-firstResult
	if first.err != nil || first.value != 1 {
		t.Fatalf("first call = %d, %v", first.value, first.err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("calls = %d, want 2", got)
	}
}

func TestDistinctComparableKeysNeverShareFlight(t *testing.T) {
	t.Parallel()
	g := singleflight.New[collidingKey, int]()
	release := make(chan struct{})
	started := make(chan int, 2)
	results := make(chan callResult, 2)
	for _, key := range []collidingKey{{id: 1}, {id: 2}} {
		go func(key collidingKey) {
			value, _, err := g.Do(context.Background(), key, func(context.Context) (int, error) {
				started <- key.id
				<-release
				return key.id, nil
			})
			results <- callResult{value: value, err: err}
		}(key)
	}

	seenStarts := make(map[int]bool, 2)
	for range 2 {
		select {
		case id := <-started:
			seenStarts[id] = true
		case <-time.After(500 * time.Millisecond):
			close(release)
			t.Fatalf("started keys = %v, want both distinct flights", seenStarts)
		}
	}
	close(release)

	seenResults := make(map[int]bool, 2)
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatalf("Do: %v", result.err)
		}
		seenResults[result.value] = true
	}
	if !seenResults[1] || !seenResults[2] {
		t.Fatalf("results = %v, want distinct values", seenResults)
	}
}
