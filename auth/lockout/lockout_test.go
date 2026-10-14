// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package lockout_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/auth/lockout"
	gerr "github.com/golusoris/golusoris/core/errors"
)

func TestService_LocksAfterMaxFails(t *testing.T) {
	t.Parallel()

	clk := clockwork.NewFakeClock()
	svc, err := lockout.New(lockout.NewMemoryStore(), clk, lockout.Options{
		MaxFails: 3,
		Window:   1 * time.Minute,
		Cooldown: 5 * time.Minute,
	})
	require.NoError(t, err)
	ctx := context.Background()

	require.NoError(t, svc.Check(ctx, "alice"))
	require.NoError(t, svc.Fail(ctx, "alice"))
	require.NoError(t, svc.Check(ctx, "alice"))
	require.NoError(t, svc.Fail(ctx, "alice"))
	require.NoError(t, svc.Fail(ctx, "alice"))

	require.Error(t, svc.Check(ctx, "alice"), "expected locked")

	// After cooldown, the lock expires.
	clk.Advance(6 * time.Minute)
	require.NoError(t, svc.Check(ctx, "alice"))
}

func TestService_ResetClearsCounter(t *testing.T) {
	t.Parallel()

	svc, err := lockout.New(lockout.NewMemoryStore(), nil, lockout.Options{MaxFails: 2, Window: time.Minute, Cooldown: time.Minute})
	require.NoError(t, err)
	ctx := context.Background()

	require.NoError(t, svc.Fail(ctx, "bob"))
	require.NoError(t, svc.Reset(ctx, "bob"))
	require.NoError(t, svc.Fail(ctx, "bob"))
	require.NoError(t, svc.Check(ctx, "bob"))
}

func TestService_RejectsBlankIdentity(t *testing.T) {
	t.Parallel()

	svc, err := lockout.New(lockout.NewMemoryStore(), nil, lockout.Options{})
	require.NoError(t, err)
	for _, operation := range []func() error{
		func() error { return svc.Check(t.Context(), " \t") },
		func() error { return svc.Fail(t.Context(), " \t") },
		func() error { return svc.Reset(t.Context(), " \t") },
	} {
		require.Error(t, operation())
	}
}

func TestService_WindowResetsCounter(t *testing.T) {
	t.Parallel()

	clk := clockwork.NewFakeClock()
	svc, err := lockout.New(lockout.NewMemoryStore(), clk, lockout.Options{
		MaxFails: 2,
		Window:   1 * time.Minute,
		Cooldown: 5 * time.Minute,
	})
	require.NoError(t, err)
	ctx := context.Background()

	require.NoError(t, svc.Fail(ctx, "carol"))
	clk.Advance(2 * time.Minute) // outside window
	require.NoError(t, svc.Fail(ctx, "carol"))
	require.NoError(t, svc.Check(ctx, "carol"), "counter should have reset")
}

func TestService_ConcurrentFailuresDoNotLoseIncrements(t *testing.T) {
	t.Parallel()

	clk := clockwork.NewFakeClock()
	store := newFailureBarrierStore(lockout.NewMemoryStoreWithClock(clk), 2)
	svc, err := lockout.New(store, clk, lockout.Options{
		MaxFails: 2,
		Window:   time.Minute,
		Cooldown: time.Minute,
	})
	require.NoError(t, err)

	results := make(chan error, 2)
	for range 2 {
		go func() { results <- svc.Fail(context.Background(), "racer") }()
	}
	store.waitAndRelease()
	for range 2 {
		require.NoError(t, <-results)
	}
	require.Error(t, svc.Check(context.Background(), "racer"))
}

func TestNewRejectsInvalidPolicy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		opts lockout.Options
	}{
		{name: "negative max fails", opts: lockout.Options{MaxFails: -1}},
		{name: "negative window", opts: lockout.Options{Window: -time.Second}},
		{name: "negative cooldown", opts: lockout.Options{Cooldown: -time.Second}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := lockout.New(lockout.NewMemoryStore(), nil, test.opts); err == nil {
				t.Fatal("New() accepted invalid policy")
			}
		})
	}
	if _, err := lockout.New(nil, nil, lockout.Options{}); err == nil {
		t.Fatal("New() accepted nil store")
	}
}

func TestNewHandlesTypedNilDependencies(t *testing.T) {
	t.Parallel()

	t.Run("store is rejected", func(t *testing.T) {
		t.Parallel()
		var store *failureBarrierStore
		_, err := lockout.New(store, nil, lockout.Options{})
		require.Error(t, err)
	})

	t.Run("optional clock uses default", func(t *testing.T) {
		t.Parallel()
		var clk *clockwork.FakeClock
		svc, err := lockout.New(lockout.NewMemoryStore(), clk, lockout.Options{})
		require.NoError(t, err)
		require.NotPanics(t, func() {
			err = svc.Fail(t.Context(), "account")
		})
		require.NoError(t, err)
	})
}

type failureBarrierStore struct {
	*lockout.MemoryStore
	ready   chan struct{}
	release chan struct{}
}

func newFailureBarrierStore(store *lockout.MemoryStore, callers int) *failureBarrierStore {
	return &failureBarrierStore{
		MemoryStore: store,
		ready:       make(chan struct{}, callers),
		release:     make(chan struct{}),
	}
}

func (s *failureBarrierStore) RecordFailure(
	ctx context.Context,
	key string,
	now time.Time,
	opts lockout.Options,
) error {
	s.ready <- struct{}{}
	<-s.release
	return s.MemoryStore.RecordFailure(ctx, key, now, opts)
}

func (s *failureBarrierStore) waitAndRelease() {
	for range cap(s.ready) {
		<-s.ready
	}
	close(s.release)
}

func TestMemoryStore_GetAndRecordFailure(t *testing.T) {
	t.Parallel()
	clk := clockwork.NewFakeClock()
	store := lockout.NewMemoryStoreWithClock(clk)
	ctx := t.Context()

	// Missing key returns not found
	_, err := store.Get(ctx, "unknown")
	require.Error(t, err)
	var ge *gerr.Error
	require.True(t, errors.As(err, &ge))
	require.Equal(t, gerr.CodeNotFound, ge.Code)

	// Record failure and verify Get returns the stored state
	now := clk.Now()
	opts := lockout.Options{MaxFails: 3, Window: 2 * time.Minute, Cooldown: 10 * time.Minute}
	err = store.RecordFailure(ctx, "user-1", now, opts)
	require.NoError(t, err)

	state, err := store.Get(ctx, "user-1")
	require.NoError(t, err)
	require.Equal(t, 1, state.Fails)
	require.Equal(t, now, state.FirstFailAt)
	require.True(t, state.LockedUntil.IsZero())
}

func TestMemoryStore_Get_ExpiredBoundary(t *testing.T) {
	t.Parallel()
	clk := clockwork.NewFakeClock()
	store := lockout.NewMemoryStoreWithClock(clk)
	ctx := t.Context()

	now := clk.Now()
	opts := lockout.Options{MaxFails: 3, Window: time.Minute, Cooldown: 5 * time.Minute}
	err := store.RecordFailure(ctx, "user-exp", now, opts)
	require.NoError(t, err)

	// At exact expiry boundary (Now == expires), entry is still active
	clk.Advance(time.Minute)
	state, err := store.Get(ctx, "user-exp")
	require.NoError(t, err)
	require.Equal(t, 1, state.Fails)

	// Just past expiry (Now > expires), entry expires and is pruned
	clk.Advance(time.Nanosecond)
	_, err = store.Get(ctx, "user-exp")
	require.Error(t, err)
	var ge *gerr.Error
	require.True(t, errors.As(err, &ge))
	require.Equal(t, gerr.CodeNotFound, ge.Code)
	require.Contains(t, err.Error(), "expired")

	// Subsequent Get confirms key is pruned
	_, err = store.Get(ctx, "user-exp")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestMemoryStore_Delete(t *testing.T) {
	t.Parallel()
	clk := clockwork.NewFakeClock()
	store := lockout.NewMemoryStoreWithClock(clk)
	ctx := t.Context()

	// Delete non-existent key is idempotent and returns nil
	require.NoError(t, store.Delete(ctx, "nonexistent"))

	// Record failure, verify present, then delete
	err := store.RecordFailure(ctx, "user-del", clk.Now(), lockout.Options{MaxFails: 2, Window: time.Minute, Cooldown: time.Minute})
	require.NoError(t, err)

	_, err = store.Get(ctx, "user-del")
	require.NoError(t, err)

	require.NoError(t, store.Delete(ctx, "user-del"))

	// Verify key no longer exists
	_, err = store.Get(ctx, "user-del")
	require.Error(t, err)
	var ge *gerr.Error
	require.True(t, errors.As(err, &ge))
	require.Equal(t, gerr.CodeNotFound, ge.Code)
}
