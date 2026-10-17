// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package store_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/idempotency/store"
)

func TestMemoryStore_ResponseCopies(t *testing.T) {
	t.Parallel()
	value := store.NewMemoryStore()
	ctx := t.Context()
	claim, err := value.Claim(ctx, "key", "fp", time.Minute)
	require.NoError(t, err)
	response := store.CachedResponse{StatusCode: http.StatusCreated, Header: http.Header{"X-Test": {"original"}}, Body: []byte("body")}
	require.NoError(t, value.Commit(ctx, "key", claim.Token, "fp", response, time.Minute))
	response.Header.Set("X-Test", "changed")
	response.Body[0] = 'X'
	for range 2 {
		replayed, claimErr := value.Claim(ctx, "key", "fp", time.Minute)
		require.NoError(t, claimErr)
		require.Equal(t, "original", replayed.Response.Header.Get("X-Test"))
		require.Equal(t, []byte("body"), replayed.Response.Body)
		replayed.Response.Header.Set("X-Test", "changed again")
		replayed.Response.Body[0] = 'Y'
	}
}

func TestMemoryStore_MinimumTTLExpiresAtBoundary(t *testing.T) {
	t.Parallel()
	clk := clockwork.NewFakeClockAt(time.Unix(0, 0))
	value := store.NewMemoryStoreWithClock(clk)
	ctx := t.Context()
	_, err := value.Claim(ctx, "key", "fp", 0)
	require.Error(t, err)
	first, err := value.Claim(ctx, "key", "fp", time.Nanosecond)
	require.NoError(t, err)
	require.Equal(t, store.ClaimAcquired, first.State)
	clk.Advance(time.Nanosecond)
	next, err := value.Claim(ctx, "key", "fp", time.Nanosecond)
	require.NoError(t, err)
	require.Equal(t, store.ClaimAcquired, next.State)
	require.NotEqual(t, first.Token, next.Token)
}
