// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package singleflight_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/golusoris/golusoris/cache/singleflight"
)

type collidingKey struct{ id int }

func (collidingKey) String() string { return "same" }

type callResult struct {
	value int
	err   error
}

func TestDoPanicReachesCaller(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		g := singleflight.New[string, int]()
		calls := 0
		const message = "singleflight caller panic"
		var recovered any
		func() {
			defer func() { recovered = recover() }()
			_, _, err := g.Do(t.Context(), "key", func(context.Context) (int, error) {
				calls++
				panic(message)
			})
			if err != nil {
				t.Errorf("Do: %v", err)
			}
		}()
		if recovered == nil || !strings.Contains(fmt.Sprint(recovered), message) {
			t.Fatalf("caller recovered = %v, want panic containing %q", recovered, message)
		}
		value, _, err := g.Do(t.Context(), "key", func(context.Context) (int, error) {
			calls++
			return 42, nil
		})
		if value != 42 || err != nil || calls != 2 {
			t.Fatalf("next Do = %d, %v, calls %d", value, err, calls)
		}
	})
}

type panicOutcome struct {
	recovered any
	err       error
}

func captureFlightPanic(ctx context.Context, call func(context.Context, string, func(context.Context) (int, error)) (int, bool, error), fn func(context.Context) (int, error)) (result panicOutcome) {
	defer func() { result.recovered = recover() }()
	_, _, result.err = call(ctx, "key", fn)
	return result
}

func TestDoPanicWithCancelableWaiter(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) { testPanicWithCancelableWaiter(t, false) })
}

func TestDoPanicAfterCancelableWaiterLeaves(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) { testPanicWithCancelableWaiter(t, true) })
}

func testPanicWithCancelableWaiter(t *testing.T, cancelBeforePanic bool) {
	t.Helper()
	g := singleflight.New[string, int]()
	gate := make(chan struct{})
	const message = "shared callback panic"
	calls := 0
	fn := func(context.Context) (int, error) { calls++; <-gate; panic(message) }
	leader, follower := make(chan panicOutcome, 1), make(chan panicOutcome, 1)
	go func() { leader <- captureFlightPanic(t.Context(), g.Do, fn) }()
	synctest.Wait()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { follower <- captureFlightPanic(ctx, g.DoCancelable, fn) }()
	synctest.Wait()
	if cancelBeforePanic {
		cancel()
		result := <-follower
		if result.recovered != nil || !errors.Is(result.err, context.Canceled) {
			t.Fatalf("cancelled follower = %+v", result)
		}
	}
	close(gate)
	checkFlightPanic(t, <-leader, message)
	if !cancelBeforePanic {
		checkFlightPanic(t, <-follower, message)
	}
	if calls != 1 {
		t.Fatalf("callback count = %d, want 1", calls)
	}
	checkDoAfterPanic(t, g)
	checkPureDoPanic(t, g)
}

func checkFlightPanic(t *testing.T, result panicOutcome, message string) {
	t.Helper()
	if result.recovered != nil || result.err == nil {
		t.Fatalf("panic outcome = %+v; want error without panic", result)
	}
	text := result.err.Error()
	if !strings.Contains(text, "singleflight: callback panic: "+message) || !strings.Contains(text, "runtime/debug.Stack") {
		t.Fatalf("panic error = %q; want %q and recovery stack", text, message)
	}
}

func TestNilInterfaceResult(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		g := singleflight.New[string, any]()
		for _, call := range []struct {
			name string
			do   func(context.Context, string, func(context.Context) (any, error)) (any, bool, error)
		}{{name: "Do", do: g.Do}, {name: "DoCancelable", do: g.DoCancelable}} {
			value, shared, err := call.do(t.Context(), "key", func(context.Context) (any, error) {
				return nil, nil //nolint:nilnil // WHY: nil is a valid interface result exercised by this regression.
			})
			if value != nil || shared || err != nil {
				t.Errorf("%s = %v, %t, %v; want nil, false, nil", call.name, value, shared, err)
			}
		}
	})
}

func checkDoAfterPanic(t *testing.T, g *singleflight.Group[string, int]) {
	t.Helper()
	value, shared, err := g.Do(t.Context(), "key", func(context.Context) (int, error) { return 42, nil })
	if value != 42 || shared || err != nil {
		t.Fatalf("next Do = %d, %t, %v; want 42, false, nil", value, shared, err)
	}
}

func checkPureDoPanic(t *testing.T, g *singleflight.Group[string, int]) {
	t.Helper()
	const message = "pure Do callback panic"
	result := captureFlightPanic(t.Context(), g.Do, func(context.Context) (int, error) { panic(message) })
	checkCallerPanic(t, result, message)
	checkDoAfterPanic(t, g)
}

func checkCallerPanic(t *testing.T, result panicOutcome, message string) {
	t.Helper()
	if result.err != nil || result.recovered == nil || !strings.Contains(fmt.Sprint(result.recovered), message) {
		t.Fatalf("panic outcome = %+v; want caller panic containing %q", result, message)
	}
}

func TestDoCancelablePanicReturnsError(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		g := singleflight.New[string, int]()
		const message = "cancelable callback panic"
		result := captureFlightPanic(t.Context(), g.DoCancelable, func(context.Context) (int, error) { panic(message) })
		checkFlightPanic(t, result, message)
		checkDoAfterPanic(t, g)
	})
}

func TestDoPanicReachesDuplicateCallers(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		g := singleflight.New[string, int]()
		gate := make(chan struct{})
		const message = "duplicate Do callback panic"
		calls := 0
		fn := func(context.Context) (int, error) { calls++; <-gate; panic(message) }
		results := make(chan panicOutcome, 2)
		go func() { results <- captureFlightPanic(t.Context(), g.Do, fn) }()
		synctest.Wait()
		go func() { results <- captureFlightPanic(t.Context(), g.Do, fn) }()
		synctest.Wait()
		close(gate)
		for range 2 {
			checkCallerPanic(t, <-results, message)
		}
		if calls != 1 {
			t.Fatalf("callback count = %d, want 1", calls)
		}
		checkDoAfterPanic(t, g)
	})
}

func TestFnErrorReturnedUnchanged(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		g := singleflight.New[string, int]()
		sentinel := errors.New("callback failed")
		fn := func(context.Context) (int, error) { return 0, sentinel }
		for _, call := range []struct {
			name string
			do   func(context.Context, string, func(context.Context) (int, error)) (int, bool, error)
		}{{name: "Do", do: g.Do}, {name: "DoCancelable", do: g.DoCancelable}} {
			value, shared, err := call.do(t.Context(), "key", fn)
			if !errors.Is(err, sentinel) || err != sentinel || value != 0 || shared { //nolint:errorlint // WHY: the contract requires error identity as well as errors.Is.
				t.Errorf("%s = %d, %t, %v; want unchanged sentinel", call.name, value, shared, err)
			}
		}
	})
}

func TestMixedDoAndCancelableRunsOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		g := singleflight.New[string, int]()
		gate := make(chan struct{})
		var mu sync.Mutex
		calls, running, peak := 0, 0, 0
		fn := func(ctx context.Context) (int, error) {
			mu.Lock()
			calls++
			running++
			peak = max(peak, running)
			mu.Unlock()
			<-gate
			mu.Lock()
			running--
			mu.Unlock()
			return 42, ctx.Err()
		}
		leader := make(chan callResult, 1)
		go func() {
			value, _, err := g.Do(t.Context(), "key", fn)
			leader <- callResult{value: value, err: err}
		}()
		synctest.Wait()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		follower := make(chan error, 1)
		go func() {
			_, _, err := g.DoCancelable(ctx, "key", fn)
			follower <- err
		}()
		synctest.Wait()
		cancel()
		if err := <-follower; !errors.Is(err, context.Canceled) {
			t.Errorf("cancelled follower: %v", err)
		}
		close(gate)
		if result := <-leader; result.value != 42 || result.err != nil {
			t.Errorf("leader = %+v", result)
		}
		synctest.Wait()
		if calls != 1 || running != 0 || peak != 1 {
			t.Errorf("calls %d, running %d, peak %d; want 1, 0, 1", calls, running, peak)
		}
		value, _, err := g.Do(t.Context(), "key", fn)
		if value != 42 || err != nil || calls != 2 {
			t.Errorf("next Do = %d, %v, calls %d, peak %d", value, err, calls, peak)
		}
	})
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

func TestDoCancelableFollower(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		g := singleflight.New[string, int]()
		gate := make(chan struct{})
		leader := make(chan callResult, 1)
		go func() {
			value, _, err := g.DoCancelable(t.Context(), "key", func(context.Context) (int, error) { <-gate; return 42, nil })
			leader <- callResult{value: value, err: err}
		}()
		synctest.Wait()
		ctx, cancel := context.WithCancel(t.Context())
		follower := make(chan callResult, 1)
		go func() {
			value, _, err := g.DoCancelable(ctx, "key", func(context.Context) (int, error) { return -1, nil })
			follower <- callResult{value: value, err: err}
		}()
		synctest.Wait()
		cancel()
		synctest.Wait()
		select {
		case result := <-follower:
			if !errors.Is(result.err, context.Canceled) || result.value != 0 {
				t.Errorf("cancelled follower = %+v", result)
			}
		default:
			t.Error("follower did not return on cancellation")
		}
		close(gate)
		if result := <-leader; result.err != nil || result.value != 42 {
			t.Errorf("leader = %+v", result)
		}
	})
}

func TestDoCancelableAlreadyCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	g := singleflight.New[string, int]()
	called := false
	value, shared, err := g.DoCancelable(ctx, "key", func(context.Context) (int, error) { called = true; return 42, nil })
	if called || value != 0 || shared || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled = %d, %t, %v, called %t", value, shared, err, called)
	}
}

func TestDoCancelableSharesValue(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		g := singleflight.New[string, int]()
		gate := make(chan struct{})
		var calls atomic.Int32
		done := make(chan callResult, 2)
		for range 2 {
			go func() {
				value, shared, err := g.DoCancelable(t.Context(), "key", func(context.Context) (int, error) { calls.Add(1); <-gate; return 42, nil })
				if !shared {
					t.Error("result not shared")
				}
				done <- callResult{value: value, err: err}
			}()
		}
		synctest.Wait()
		close(gate)
		for range 2 {
			if result := <-done; result.value != 42 || result.err != nil {
				t.Errorf("result = %+v", result)
			}
		}
		if calls.Load() != 1 {
			t.Errorf("calls = %d", calls.Load())
		}
	})
}

func TestDoCancelableLastWaiterLeavesRunningFlight(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		g := singleflight.New[string, int]()
		gate := make(chan struct{})
		var calls atomic.Int32
		fn := func(context.Context) (int, error) { calls.Add(1); <-gate; return 42, t.Context().Err() }
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		first := make(chan callResult, 1)
		go func() {
			value, _, err := g.DoCancelable(ctx, "key", fn)
			first <- callResult{value: value, err: err}
		}()
		synctest.Wait()
		cancel()
		if result := <-first; !errors.Is(result.err, context.Canceled) {
			t.Errorf("cancelled waiter = %+v", result)
		}
		next := make(chan callResult, 1)
		go func() {
			value, _, err := g.DoCancelable(t.Context(), "key", fn)
			next <- callResult{value: value, err: err}
		}()
		synctest.Wait()
		if got := calls.Load(); got != 1 {
			t.Errorf("running callback count = %d, want 1", got)
		}
		close(gate)
		if result := <-next; result.value != 42 || result.err != nil {
			t.Errorf("live waiter = %+v", result)
		}
		value, _, err := g.Do(t.Context(), "key", func(context.Context) (int, error) {
			calls.Add(1)
			return 84, nil
		})
		if value != 84 || err != nil || calls.Load() != 2 {
			t.Errorf("finished callback reused: value %d, err %v, calls %d", value, err, calls.Load())
		}
	})
}

func TestForgetOldCallbackKeepsReplacement(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		g := singleflight.New[string, int]()
		oldGate, newGate := make(chan struct{}), make(chan struct{})
		var calls atomic.Int32
		start := func(gate chan struct{}, value int) <-chan callResult {
			done := make(chan callResult, 1)
			go func() {
				got, _, err := g.DoCancelable(t.Context(), "key", func(context.Context) (int, error) {
					calls.Add(1)
					<-gate
					return value, nil
				})
				done <- callResult{value: got, err: err}
			}()
			synctest.Wait()
			return done
		}
		old := start(oldGate, 1)
		g.Forget("key")
		replacement := start(newGate, 2)
		close(oldGate)
		if result := <-old; result.value != 1 || result.err != nil {
			t.Errorf("old callback = %+v", result)
		}
		joined := start(newGate, 3)
		if got := calls.Load(); got != 2 {
			t.Errorf("callback count = %d, want 2", got)
		}
		close(newGate)
		for _, done := range []<-chan callResult{replacement, joined} {
			if result := <-done; result.value != 2 || result.err != nil {
				t.Errorf("replacement waiter = %+v", result)
			}
		}
	})
}
