// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package idempotency_test

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	"github.com/redis/rueidis"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/idempotency"
	redistest "github.com/golusoris/golusoris/testutil/redis"
)

// maxAdvanceScans bounds the SCAN pages one simulated clock advance walks.
const maxAdvanceScans = 1000

// newRedisHarness isolates each case under its own key prefix and simulates
// a clock advance by shortening every PTTL under that prefix by d.
func newRedisHarness(client rueidis.Client) func(t *testing.T) storeHarness {
	return func(t *testing.T) storeHarness {
		t.Helper()
		prefix := "conformance:" + rand.Text() + ":"
		store, err := idempotency.NewRedisStore(client, prefix)
		require.NoError(t, err)
		return storeHarness{store: store, advance: func(t *testing.T, d time.Duration) {
			t.Helper()
			advanceRedis(t, client, prefix, d)
		}}
	}
}

func advanceRedis(t *testing.T, client rueidis.Client, prefix string, d time.Duration) {
	t.Helper()
	ctx := context.Background()
	cursor := uint64(0)
	for range maxAdvanceScans {
		entry, err := client.Do(ctx, client.B().Scan().Cursor(cursor).Match(prefix+"*").Count(100).Build()).AsScanEntry()
		require.NoError(t, err)
		for _, key := range entry.Elements {
			remaining, err := client.Do(ctx, client.B().Pttl().Key(key).Build()).AsInt64()
			require.NoError(t, err)
			left := remaining - d.Milliseconds()
			if left <= 0 {
				require.NoError(t, client.Do(ctx, client.B().Del().Key(key).Build()).Error())
				continue
			}
			require.NoError(t, client.Do(ctx, client.B().Pexpire().Key(key).Milliseconds(left).Build()).Error())
		}
		if entry.Cursor == 0 {
			return
		}
		cursor = entry.Cursor
	}
	t.Fatalf("redis SCAN did not finish in %d pages", maxAdvanceScans)
}

func TestRedisStore_Conformance(t *testing.T) {
	t.Parallel()
	runStoreConformance(t, newRedisHarness(redistest.Start(t)))
}

// TestRedisStore_SharedAcrossReplicas is the #624 acceptance over Redis.
func TestRedisStore_SharedAcrossReplicas(t *testing.T) {
	t.Parallel()
	client := redistest.Start(t)
	replicaA, err := idempotency.NewRedisStore(client, "shared:")
	require.NoError(t, err)
	replicaB, err := idempotency.NewRedisStore(client, "shared:")
	require.NoError(t, err)
	requireSharedAcrossReplicas(t, replicaA, replicaB)
}

// TestRedisStore_RecordsExpireServerSide asserts the PX bound: Redis drops
// the record itself, so no sweeper runs.
func TestRedisStore_RecordsExpireServerSide(t *testing.T) {
	t.Parallel()
	client := redistest.Start(t)
	store, err := idempotency.NewRedisStore(client, "")
	require.NoError(t, err)
	_, err = store.Claim(t.Context(), "key", fingerprintA, 90*time.Second)
	require.NoError(t, err)
	ttl, err := client.Do(t.Context(), client.B().Pttl().Key(idempotency.DefaultRedisPrefix+"key").Build()).AsInt64()
	require.NoError(t, err)
	require.Greater(t, ttl, int64(60_000))
	require.LessOrEqual(t, ttl, int64(90_000))
	_, isSweeper := any(store).(idempotency.Sweeper)
	require.False(t, isSweeper)
}

func TestRedisStore_WrongTypeKeyFails(t *testing.T) {
	t.Parallel()
	client := redistest.Start(t)
	store, err := idempotency.NewRedisStore(client, "wrong:")
	require.NoError(t, err)
	require.NoError(t, client.Do(t.Context(), client.B().Set().Key("wrong:key").Value("plain").Build()).Error())
	_, err = store.Claim(t.Context(), "key", fingerprintA, time.Minute)
	require.Error(t, err)
}

func TestNewRedisStore_NilClient(t *testing.T) {
	t.Parallel()
	store, err := idempotency.NewRedisStore(nil, "")
	require.Error(t, err)
	require.Nil(t, store)
}
