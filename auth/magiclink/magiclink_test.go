// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package magiclink_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/auth/magiclink"
	gerr "github.com/golusoris/golusoris/core/errors"
)

var testHMACSecret = strings.Repeat("x", 32)

func TestNew_EmptySecret(t *testing.T) {
	t.Parallel()
	for _, size := range []int{0, 1, 31} {
		_, err := magiclink.New(magiclink.NewMemoryStore(), nil, make([]byte, size), 0)
		require.Error(t, err)
	}
	_, err := magiclink.New(magiclink.NewMemoryStore(), nil, make([]byte, 32), 0)
	require.NoError(t, err)
}

func TestNew_RejectsInvalidDependenciesAndTTL(t *testing.T) {
	t.Parallel()
	_, err := magiclink.New(nil, nil, []byte(testHMACSecret), time.Minute)
	require.Error(t, err)
	_, err = magiclink.New(magiclink.NewMemoryStore(), nil, []byte(testHMACSecret), -time.Second)
	require.Error(t, err)
}

func TestNew_HandlesTypedNilDependencies(t *testing.T) {
	t.Parallel()

	t.Run("store is rejected", func(t *testing.T) {
		t.Parallel()
		var store *magiclink.MemoryStore
		_, err := magiclink.New(store, nil, []byte(testHMACSecret), time.Minute)
		require.Error(t, err)
	})

	t.Run("optional clock uses default", func(t *testing.T) {
		t.Parallel()
		var clk *clockwork.FakeClock
		svc, err := magiclink.New(
			magiclink.NewMemoryStore(),
			clk,
			[]byte(testHMACSecret),
			time.Minute,
		)
		require.NoError(t, err)
		require.NotPanics(t, func() {
			_, err = svc.Issue(t.Context(), "user@example.com")
		})
		require.NoError(t, err)
	})
}

func TestNewMemoryStoreDefaultsTypedNilClock(t *testing.T) {
	t.Parallel()

	var typedNilClock *clockwork.FakeClock
	store := magiclink.NewMemoryStoreWithClock(typedNilClock)
	service, err := magiclink.New(store, nil, []byte(testHMACSecret), time.Minute)
	require.NoError(t, err)
	token, err := service.Issue(t.Context(), "clock@example.com")
	require.NoError(t, err)
	_, err = service.Verify(t.Context(), token)
	require.NoError(t, err)
}

func TestService_HappyPath(t *testing.T) {
	t.Parallel()

	clk := clockwork.NewFakeClock()
	svc, err := magiclink.New(magiclink.NewMemoryStore(), clk, []byte(testHMACSecret), 5*time.Minute)
	require.NoError(t, err)

	tok, err := svc.Issue(context.Background(), "alice@example.com")
	require.NoError(t, err)

	email, err := svc.Verify(context.Background(), tok)
	require.NoError(t, err)
	require.Equal(t, "alice@example.com", email)

	_, err = svc.Verify(context.Background(), tok)
	requireUnauthorized(t, err)
}

func TestService_Expiry(t *testing.T) {
	t.Parallel()

	clk := clockwork.NewFakeClock()
	svc, err := magiclink.New(magiclink.NewMemoryStore(), clk, []byte(testHMACSecret), 1*time.Minute)
	require.NoError(t, err)

	tok, err := svc.Issue(context.Background(), "bob@example.com")
	require.NoError(t, err)

	clk.Advance(time.Minute)
	_, err = svc.Verify(context.Background(), tok)
	requireUnauthorized(t, err)
}

func TestService_ClonesSecret(t *testing.T) {
	t.Parallel()

	secret := []byte(testHMACSecret)
	svc, err := magiclink.New(magiclink.NewMemoryStore(), nil, secret, time.Minute)
	require.NoError(t, err)
	token, err := svc.Issue(context.Background(), "clone@example.com")
	require.NoError(t, err)
	secret[0] = 'y'
	_, err = svc.Verify(context.Background(), token)
	require.NoError(t, err)
}

func TestService_RejectsEmptyEmail(t *testing.T) {
	t.Parallel()
	svc, err := magiclink.New(magiclink.NewMemoryStore(), nil, []byte(testHMACSecret), 0)
	require.NoError(t, err)
	_, err = svc.Issue(context.Background(), "  ")
	require.Error(t, err)
}

func TestService_ConcurrentVerifyOnlyOneSucceeds(t *testing.T) {
	t.Parallel()

	store := newConsumeBarrierStore(2)
	svc, err := magiclink.New(store, nil, []byte(testHMACSecret), time.Minute)
	require.NoError(t, err)
	token, err := svc.Issue(context.Background(), "alice@example.com")
	require.NoError(t, err)

	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, verifyErr := svc.Verify(context.Background(), token)
			results <- verifyErr
		}()
	}
	for range 2 {
		<-store.ready
	}
	close(store.release)

	successes := 0
	failures := 0
	for range 2 {
		if verifyErr := <-results; verifyErr == nil {
			successes++
		} else {
			requireUnauthorized(t, verifyErr)
			failures++
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, failures)
}

func requireUnauthorized(t *testing.T, err error) {
	t.Helper()
	var coded *gerr.Error
	require.ErrorAs(t, err, &coded)
	require.Equal(t, gerr.CodeUnauthorized, coded.Code)
}

type consumeBarrierStore struct {
	store   *magiclink.MemoryStore
	ready   chan struct{}
	release chan struct{}
}

func newConsumeBarrierStore(callers int) *consumeBarrierStore {
	return &consumeBarrierStore{
		store:   magiclink.NewMemoryStore(),
		ready:   make(chan struct{}, callers),
		release: make(chan struct{}),
	}
}

func (s *consumeBarrierStore) Save(ctx context.Context, link magiclink.Link) error {
	return s.store.Save(ctx, link)
}

func (s *consumeBarrierStore) Consume(ctx context.Context, hash []byte) (magiclink.Link, error) {
	s.ready <- struct{}{}
	<-s.release
	return s.store.Consume(ctx, hash)
}
