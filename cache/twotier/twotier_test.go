package twotier

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golusoris/golusoris/cache/memory"
	"github.com/golusoris/golusoris/cache/singleflight"
)

// stubL2 is an in-memory [l2] for hermetic tests (no real Redis). It records
// call counts and can be made to fail to exercise the fall-through paths.
type stubL2 struct {
	mu             sync.Mutex
	data           map[string][]byte
	getCalls       atomic.Int32
	setCalls       atomic.Int32
	delCalls       atomic.Int32
	delPrefixCalls atomic.Int32
	failGet        bool
	failSet        bool
	failDel        bool
	failDelPrefix  bool
}

func newStubL2() *stubL2 { return &stubL2{data: make(map[string][]byte)} }

var errStub = errors.New("stub l2 failure")

func (s *stubL2) Get(_ context.Context, key string) ([]byte, bool, error) {
	s.getCalls.Add(1)
	if s.failGet {
		return nil, false, errStub
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[key]
	return v, ok, nil
}

func (s *stubL2) Set(_ context.Context, key string, val []byte, _ time.Duration) error {
	s.setCalls.Add(1)
	if s.failSet {
		return errStub
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = val
	return nil
}

func (s *stubL2) Del(_ context.Context, key string) error {
	s.delCalls.Add(1)
	if s.failDel {
		return errStub
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	return nil
}

func (s *stubL2) DelPrefix(_ context.Context, prefix string) error {
	s.delPrefixCalls.Add(1)
	if s.failDelPrefix {
		return errStub
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.data {
		if strings.HasPrefix(k, prefix) {
			delete(s.data, k)
		}
	}
	return nil
}

// newTestTwoTier builds a TwoTier backed by an otter L1 and the given stub L2.
func newTestTwoTier(t *testing.T, l2 l2) *TwoTier {
	t.Helper()
	l1, err := memory.NewForTest(100, 0)
	if err != nil {
		t.Fatalf("memory.NewForTest: %v", err)
	}
	return &TwoTier{
		l1:     l1,
		l2:     l2,
		logger: slog.New(slog.DiscardHandler),
		l2TTL:  time.Minute,
		group:  singleflight.New[string, []byte](),
	}
}

func TestGet_loaderFallbackPopulatesBothTiers(t *testing.T) {
	t.Parallel()
	l2 := newStubL2()
	tt := newTestTwoTier(t, l2)
	view := NewTyped[int](tt, "n")

	var loads atomic.Int32
	loader := func(_ context.Context) (int, error) {
		loads.Add(1)
		return 42, nil
	}

	v, err := view.Get(context.Background(), "a", loader)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v != 42 {
		t.Fatalf("got %d, want 42", v)
	}
	if loads.Load() != 1 {
		t.Fatalf("loader called %d times, want 1", loads.Load())
	}
	// L2 was populated on the miss.
	if _, ok, _ := l2.Get(context.Background(), "n:a"); !ok {
		t.Error("expected L2 to be populated after loader hit")
	}
	// L1 was populated: a second Get must not call the loader.
	if _, err := view.Get(context.Background(), "a", loader); err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if loads.Load() != 1 {
		t.Errorf("loader called %d times after L1 hit, want 1", loads.Load())
	}
}

func TestGet_l2HitBackfillsL1AndSkipsLoader(t *testing.T) {
	t.Parallel()
	l2 := newStubL2()
	tt := newTestTwoTier(t, l2)
	view := NewTyped[string](tt, "s")

	// Seed L2 only (JSON-encoded), L1 empty.
	if err := l2.Set(context.Background(), "s:k", []byte(`"hello"`), time.Minute); err != nil {
		t.Fatal(err)
	}

	loader := func(_ context.Context) (string, error) {
		t.Error("loader must not run on an L2 hit")
		return "", nil
	}

	v, err := view.Get(context.Background(), "k", loader)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v != "hello" {
		t.Fatalf("got %q, want %q", v, "hello")
	}
	// L1 back-filled: next Get reads from L1 (no extra L2 get).
	before := l2.getCalls.Load()
	if _, err := view.Get(context.Background(), "k", loader); err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if l2.getCalls.Load() != before {
		t.Errorf("L2 get called again (%d → %d); L1 back-fill failed", before, l2.getCalls.Load())
	}
}

func TestGet_l2FailureFallsThroughToLoader(t *testing.T) {
	t.Parallel()
	l2 := newStubL2()
	l2.failGet = true
	l2.failSet = true // also ensure a failing L2 set does not fail the Get
	tt := newTestTwoTier(t, l2)
	view := NewTyped[int](tt, "n")

	v, err := view.Get(context.Background(), "a", func(_ context.Context) (int, error) {
		return 7, nil
	})
	if err != nil {
		t.Fatalf("Get should tolerate L2 failure, got %v", err)
	}
	if v != 7 {
		t.Errorf("got %d, want 7", v)
	}
}

func TestGet_loaderErrorPropagates(t *testing.T) {
	t.Parallel()
	tt := newTestTwoTier(t, newStubL2())
	view := NewTyped[int](tt, "n")

	wantErr := errors.New("origin down")
	_, err := view.Get(context.Background(), "a", func(_ context.Context) (int, error) {
		return 0, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("got %v, want wrap of %v", err, wantErr)
	}
}

func TestGet_singleflightDeduplicatesLoader(t *testing.T) {
	t.Parallel()
	l2 := newStubL2()
	tt := newTestTwoTier(t, l2)
	view := NewTyped[int](tt, "n")

	var loads atomic.Int32
	var loader Loader[int] = func(_ context.Context) (int, error) {
		loads.Add(1)
		time.Sleep(20 * time.Millisecond) // widen the dedup window
		return 99, nil
	}

	const n = 20
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if v, err := view.Get(context.Background(), "k", loader); err != nil || v != 99 {
				t.Errorf("Get = (%d, %v), want (99, nil)", v, err)
			}
		}()
	}
	wg.Wait()

	if got := loads.Load(); got >= n {
		t.Errorf("loader ran %d times for %d concurrent gets; singleflight did not dedup", got, n)
	}
}

func TestSet_writesThroughToBothTiers(t *testing.T) {
	t.Parallel()
	l2 := newStubL2()
	tt := newTestTwoTier(t, l2)
	view := NewTyped[int](tt, "n")

	if err := view.Set(context.Background(), "a", 5); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// L1 hit without loader.
	v, err := view.Get(context.Background(), "a", func(_ context.Context) (int, error) {
		t.Error("loader must not run after Set populated L1")
		return 0, nil
	})
	if err != nil || v != 5 {
		t.Fatalf("Get after Set = (%d, %v), want (5, nil)", v, err)
	}
	if _, ok, _ := l2.Get(context.Background(), "n:a"); !ok {
		t.Error("Set did not write through to L2")
	}
}

func TestSet_l2FailurePropagates(t *testing.T) {
	t.Parallel()
	l2 := newStubL2()
	l2.failSet = true
	tt := newTestTwoTier(t, l2)
	view := NewTyped[int](tt, "n")

	if err := view.Set(context.Background(), "a", 1); !errors.Is(err, errStub) {
		t.Fatalf("Set = %v, want wrap of errStub", err)
	}
}

func TestDelete_removesFromBothTiers(t *testing.T) {
	t.Parallel()
	l2 := newStubL2()
	tt := newTestTwoTier(t, l2)
	view := NewTyped[int](tt, "n")

	if err := view.Set(context.Background(), "a", 3); err != nil {
		t.Fatal(err)
	}
	if err := view.Delete(context.Background(), "a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok, _ := l2.Get(context.Background(), "n:a"); ok {
		t.Error("Delete did not remove from L2")
	}
	// L1 miss now triggers the loader.
	var ran bool
	if _, err := view.Get(context.Background(), "a", func(_ context.Context) (int, error) {
		ran = true
		return 0, nil
	}); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Error("Delete did not remove from L1 (loader did not run)")
	}
}

func TestInvalidatePrefix_evictsMatchingFromBothTiers(t *testing.T) {
	t.Parallel()
	l2 := newStubL2()
	tt := newTestTwoTier(t, l2)
	view := NewTyped[int](tt, "n")
	ctx := context.Background()

	// Seed three keys sharing a "user/" sub-prefix plus one that does not.
	for _, k := range []string{"user/1", "user/2", "other/1"} {
		if err := view.Set(ctx, k, 1); err != nil {
			t.Fatalf("Set %q: %v", k, err)
		}
	}

	if err := view.InvalidatePrefix(ctx, "user/"); err != nil {
		t.Fatalf("InvalidatePrefix: %v", err)
	}

	// L2: matching keys gone, the sibling survives.
	for _, k := range []string{"n:user/1", "n:user/2"} {
		if _, ok, _ := l2.Get(ctx, k); ok {
			t.Errorf("L2 key %q survived InvalidatePrefix", k)
		}
	}
	if _, ok, _ := l2.Get(ctx, "n:other/1"); !ok {
		t.Error("InvalidatePrefix evicted a non-matching L2 key")
	}

	// L1: a matching key now misses (loader runs), the sibling still hits.
	var ran bool
	if _, err := view.Get(ctx, "user/1", func(_ context.Context) (int, error) {
		ran = true
		return 0, nil
	}); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Error("InvalidatePrefix did not evict matching key from L1")
	}
	if _, err := view.Get(ctx, "other/1", func(_ context.Context) (int, error) {
		t.Error("loader ran for a non-matching key; L1 sibling was wrongly evicted")
		return 0, nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidatePrefix_isolatesAcrossViews(t *testing.T) {
	t.Parallel()
	l2 := newStubL2()
	tt := newTestTwoTier(t, l2)
	a := NewTyped[int](tt, "ns-a")
	b := NewTyped[int](tt, "ns-b")
	ctx := context.Background()

	if err := a.Set(ctx, "k", 1); err != nil {
		t.Fatal(err)
	}
	if err := b.Set(ctx, "k", 2); err != nil {
		t.Fatal(err)
	}

	// Clearing the whole "ns-a" view (empty user prefix) must not touch ns-b:
	// the composed L2 pattern is "ns-a:*", which excludes "ns-b:k".
	if err := a.InvalidatePrefix(ctx, ""); err != nil {
		t.Fatalf("InvalidatePrefix: %v", err)
	}
	if _, ok, _ := l2.Get(ctx, "ns-a:k"); ok {
		t.Error("ns-a key survived its own InvalidatePrefix")
	}
	if _, ok, _ := l2.Get(ctx, "ns-b:k"); !ok {
		t.Error("InvalidatePrefix on ns-a leaked into ns-b")
	}
}

func TestInvalidatePrefix_l2FailurePropagates(t *testing.T) {
	t.Parallel()
	l2 := newStubL2()
	l2.failDelPrefix = true
	tt := newTestTwoTier(t, l2)
	view := NewTyped[int](tt, "n")

	if err := view.InvalidatePrefix(context.Background(), "x"); !errors.Is(err, errStub) {
		t.Fatalf("InvalidatePrefix = %v, want wrap of errStub", err)
	}
}

func TestInvalidatePrefix_nilCacheIsNoop(t *testing.T) {
	t.Parallel()
	view := NewTyped[int](nil, "n")
	if err := view.InvalidatePrefix(context.Background(), "x"); err != nil {
		t.Errorf("InvalidatePrefix on nil cache = %v, want nil", err)
	}
	var tt *TwoTier
	if err := tt.InvalidatePrefix(context.Background(), "x"); err != nil {
		t.Errorf("(*TwoTier)(nil).InvalidatePrefix = %v, want nil", err)
	}
}

func TestPrefixIsolation(t *testing.T) {
	t.Parallel()
	tt := newTestTwoTier(t, newStubL2())
	a := NewTyped[int](tt, "ns-a")
	b := NewTyped[int](tt, "ns-b")

	if err := a.Set(context.Background(), "k", 1); err != nil {
		t.Fatal(err)
	}
	if err := b.Set(context.Background(), "k", 2); err != nil {
		t.Fatal(err)
	}

	va, _ := a.Get(context.Background(), "k", func(_ context.Context) (int, error) { return -1, nil })
	vb, _ := b.Get(context.Background(), "k", func(_ context.Context) (int, error) { return -1, nil })
	if va != 1 || vb != 2 {
		t.Errorf("prefix isolation broken: a=%d b=%d", va, vb)
	}
}

func TestDisabledMode_nilCacheIsPassthrough(t *testing.T) {
	t.Parallel()
	view := NewTyped[int](nil, "n")
	ctx := context.Background()

	var loads atomic.Int32
	v, err := view.Get(ctx, "a", func(_ context.Context) (int, error) {
		loads.Add(1)
		return 11, nil
	})
	if err != nil || v != 11 {
		t.Fatalf("Get on nil cache = (%d, %v), want (11, nil)", v, err)
	}
	// Disabled cache never caches: every Get hits the loader.
	if _, err := view.Get(ctx, "a", func(_ context.Context) (int, error) {
		loads.Add(1)
		return 11, nil
	}); err != nil {
		t.Fatal(err)
	}
	if loads.Load() != 2 {
		t.Errorf("loader ran %d times, want 2 (disabled cache must not cache)", loads.Load())
	}
	// Set/Delete are no-ops, not errors.
	if err := view.Set(ctx, "a", 1); err != nil {
		t.Errorf("Set on nil cache = %v, want nil", err)
	}
	if err := view.Delete(ctx, "a"); err != nil {
		t.Errorf("Delete on nil cache = %v, want nil", err)
	}
}

func TestDefaultOptions(t *testing.T) {
	t.Parallel()
	opts := defaultOptions()
	if opts.L1TTL != time.Minute {
		t.Errorf("L1TTL = %v, want 1m", opts.L1TTL)
	}
	if opts.L2TTL != 5*time.Minute {
		t.Errorf("L2TTL = %v, want 5m", opts.L2TTL)
	}
}
