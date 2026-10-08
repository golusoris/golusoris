// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package twotier

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/cache/memory"
)

// typedViewModes builds the same typed-view contract over a stub L2 and over
// the L1-only mode that cache.twotier.l2 = none selects.
func typedViewModes() map[string]func(t *testing.T) *TwoTier {
	return map[string]func(t *testing.T) *TwoTier{
		"stub L2": func(t *testing.T) *TwoTier {
			t.Helper()
			return newTestTwoTier(t, newStubL2())
		},
		"L1 only": newL1OnlyTwoTier,
	}
}

func newL1OnlyTwoTier(t *testing.T) *TwoTier {
	t.Helper()
	l1, err := memory.NewForTest(100, 0)
	require.NoError(t, err)
	tt, err := New(l1, Options{L2: L2None}, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	return tt
}

type viewCase struct {
	name string
	run  func(t *testing.T, tt *TwoTier)
}

func typedViewContract() []viewCase {
	return []viewCase{
		{"loader fills L1 once", viewLoaderFillsL1},
		{"loader error propagates", viewLoaderError},
		{"singleflight dedupes loads", viewSingleflight},
		{"set serves without loader", viewSetServes},
		{"delete forces reload", viewDeleteReloads},
		{"set fences in-flight load", viewSetFences},
		{"delete fences in-flight load", viewDeleteFences},
		{"prefix invalidation keeps siblings", viewPrefixKeepsSiblings},
		{"prefix invalidation isolates views", viewPrefixIsolatesViews},
		{"views do not collide", viewPrefixIsolation},
	}
}

func TestTypedViewContract(t *testing.T) {
	t.Parallel()
	for mode, build := range typedViewModes() {
		for _, tc := range typedViewContract() {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				tc.run(t, build(t))
			})
		}
	}
}

// countingLoader returns value and counts calls.
func countingLoader(calls *atomic.Int32, value int) Loader[int] {
	return func(context.Context) (int, error) {
		calls.Add(1)
		return value, nil
	}
}

func viewLoaderFillsL1(t *testing.T, tt *TwoTier) {
	t.Helper()
	view := NewTyped[int](tt, "n")
	var loads atomic.Int32
	for range 2 {
		value, err := view.Get(context.Background(), "a", countingLoader(&loads, 42))
		require.NoError(t, err)
		require.Equal(t, 42, value)
	}
	require.EqualValues(t, 1, loads.Load())
}

func viewLoaderError(t *testing.T, tt *TwoTier) {
	t.Helper()
	wantErr := errors.New("origin down")
	_, err := NewTyped[int](tt, "n").Get(context.Background(), "a", func(context.Context) (int, error) {
		return 0, wantErr
	})
	require.ErrorIs(t, err, wantErr)
}

func viewSingleflight(t *testing.T, tt *TwoTier) {
	t.Helper()
	view := NewTyped[int](tt, "n")
	release := make(chan struct{})
	var loads atomic.Int32
	loader := Loader[int](func(context.Context) (int, error) {
		loads.Add(1)
		<-release
		return 99, nil
	})
	const callers = 8
	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() {
			value, err := view.Get(context.Background(), "k", loader)
			if err != nil || value != 99 {
				t.Errorf("Get = (%d, %v); want (99, nil)", value, err)
			}
		})
	}
	require.Eventually(t, func() bool { return loads.Load() == 1 }, 5*time.Second, time.Millisecond)
	time.Sleep(10 * time.Millisecond) // let the other callers join the flight
	close(release)
	wg.Wait()
	require.Less(t, loads.Load(), int32(callers))
}

func viewSetServes(t *testing.T, tt *TwoTier) {
	t.Helper()
	view := NewTyped[int](tt, "n")
	require.NoError(t, view.Set(context.Background(), "a", 5))
	var loads atomic.Int32
	value, err := view.Get(context.Background(), "a", countingLoader(&loads, 0))
	require.NoError(t, err)
	require.Equal(t, 5, value)
	require.Zero(t, loads.Load())
}

func viewDeleteReloads(t *testing.T, tt *TwoTier) {
	t.Helper()
	view := NewTyped[int](tt, "n")
	require.NoError(t, view.Set(context.Background(), "a", 3))
	require.NoError(t, view.Delete(context.Background(), "a"))
	var loads atomic.Int32
	value, err := view.Get(context.Background(), "a", countingLoader(&loads, 4))
	require.NoError(t, err)
	require.Equal(t, 4, value)
	require.EqualValues(t, 1, loads.Load())
}

// inFlightLoad starts a Get whose loader blocks until release closes and
// returns 1; done reports the Get's error.
func inFlightLoad(view *Typed[int]) (release chan struct{}, done chan error) {
	started := make(chan struct{})
	release = make(chan struct{})
	done = make(chan error, 1)
	go func() {
		_, err := view.Get(context.Background(), "a", func(context.Context) (int, error) {
			close(started)
			<-release
			return 1, nil
		})
		done <- err
	}()
	<-started
	return release, done
}

func viewSetFences(t *testing.T, tt *TwoTier) {
	t.Helper()
	view := NewTyped[int](tt, "n")
	release, done := inFlightLoad(view)
	require.NoError(t, view.Set(context.Background(), "a", 2))
	close(release)
	require.NoError(t, <-done)
	var loads atomic.Int32
	value, err := view.Get(context.Background(), "a", countingLoader(&loads, 0))
	require.NoError(t, err)
	require.Equal(t, 2, value)
	require.Zero(t, loads.Load())
}

func viewDeleteFences(t *testing.T, tt *TwoTier) {
	t.Helper()
	view := NewTyped[int](tt, "n")
	release, done := inFlightLoad(view)
	require.NoError(t, view.Delete(context.Background(), "a"))
	close(release)
	require.NoError(t, <-done)
	var loads atomic.Int32
	value, err := view.Get(context.Background(), "a", countingLoader(&loads, 3))
	require.NoError(t, err)
	require.Equal(t, 3, value)
	require.EqualValues(t, 1, loads.Load())
}

func viewPrefixKeepsSiblings(t *testing.T, tt *TwoTier) {
	t.Helper()
	view := NewTyped[int](tt, "n")
	ctx := context.Background()
	for _, k := range []string{"user/1", "user/2", "other/1"} {
		require.NoError(t, view.Set(ctx, k, 1))
	}
	require.NoError(t, view.InvalidatePrefix(ctx, "user/"))
	var loads atomic.Int32
	for _, k := range []string{"user/1", "user/2", "other/1"} {
		_, err := view.Get(ctx, k, countingLoader(&loads, 0))
		require.NoError(t, err)
	}
	require.EqualValues(t, 2, loads.Load(), "only the two user/ keys reload")
}

func viewPrefixIsolatesViews(t *testing.T, tt *TwoTier) {
	t.Helper()
	a, b := NewTyped[int](tt, "ns-a"), NewTyped[int](tt, "ns-b")
	ctx := context.Background()
	require.NoError(t, a.Set(ctx, "k", 1))
	require.NoError(t, b.Set(ctx, "k", 2))
	require.NoError(t, a.InvalidatePrefix(ctx, ""))
	var loads atomic.Int32
	value, err := b.Get(ctx, "k", countingLoader(&loads, 0))
	require.NoError(t, err)
	require.Equal(t, 2, value)
	require.Zero(t, loads.Load())
	_, err = a.Get(ctx, "k", countingLoader(&loads, 0))
	require.NoError(t, err)
	require.EqualValues(t, 1, loads.Load())
}

func viewPrefixIsolation(t *testing.T, tt *TwoTier) {
	t.Helper()
	a, b := NewTyped[int](tt, "ns-a"), NewTyped[int](tt, "ns-b")
	require.NoError(t, a.Set(context.Background(), "k", 1))
	require.NoError(t, b.Set(context.Background(), "k", 2))
	var loads atomic.Int32
	va, err := a.Get(context.Background(), "k", countingLoader(&loads, -1))
	require.NoError(t, err)
	vb, err := b.Get(context.Background(), "k", countingLoader(&loads, -1))
	require.NoError(t, err)
	require.Equal(t, [2]int{1, 2}, [2]int{va, vb})
}

func TestL1Only_RefusesEmptyComposedPrefix(t *testing.T) {
	t.Parallel()
	require.Error(t, newL1OnlyTwoTier(t).InvalidatePrefix(context.Background(), ""))
}

func TestL1Only_ExpiresWithL1TTL(t *testing.T) {
	t.Parallel()
	l1, err := memory.NewForTest(100, 0)
	require.NoError(t, err)
	tt, err := New(l1, Options{L2: L2None, L1TTL: 20 * time.Millisecond}, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	view := NewTyped[int](tt, "n")
	require.NoError(t, view.Set(context.Background(), "a", 1))
	var loads atomic.Int32
	require.Eventually(t, func() bool {
		value, getErr := view.Get(context.Background(), "a", countingLoader(&loads, 2))
		return getErr == nil && value == 2
	}, 5*time.Second, 5*time.Millisecond, "L1-only entries expire, then the loader refills them")
}
