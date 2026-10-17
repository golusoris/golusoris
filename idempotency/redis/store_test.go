// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package redis_test

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	"github.com/redis/rueidis"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/idempotency/internal/storetest"
	"github.com/golusoris/golusoris/idempotency/redis"
	"github.com/golusoris/golusoris/idempotency/store"
	redistest "github.com/golusoris/golusoris/testutil/redis"
)

// maxAdvanceScans bounds the SCAN pages one simulated clock advance walks.
const maxAdvanceScans = 1000

// newRedisHarness isolates each case under its own key prefix and simulates
// a clock advance by shortening every PTTL under that prefix by d.
func newRedisHarness(client rueidis.Client) func(t *testing.T) storetest.Harness {
	return func(t *testing.T) storetest.Harness {
		t.Helper()
		prefix := "conformance:" + rand.Text() + ":"
		store, err := redis.New(client, prefix)
		require.NoError(t, err)
		return storetest.Harness{Store: store, Advance: func(t *testing.T, d time.Duration) {
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

func TestStore_Conformance(t *testing.T) {
	t.Parallel()
	storetest.Run(t, newRedisHarness(redistest.Start(t)))
}

// TestStore_SharedAcrossReplicas is the #624 acceptance over Redis.
func TestStore_SharedAcrossReplicas(t *testing.T) {
	t.Parallel()
	client := redistest.Start(t)
	replicaA, err := redis.New(client, "shared:")
	require.NoError(t, err)
	replicaB, err := redis.New(client, "shared:")
	require.NoError(t, err)
	storetest.SharedAcrossReplicas(t, replicaA, replicaB)
}

// TestStore_RecordsExpireServerSide asserts the PX bound: Redis drops
// the record itself, so no sweeper runs.
func TestStore_RecordsExpireServerSide(t *testing.T) {
	t.Parallel()
	client := redistest.Start(t)
	value, err := redis.New(client, "")
	require.NoError(t, err)
	_, err = value.Claim(t.Context(), "key", "fingerprint-a", 90*time.Second)
	require.NoError(t, err)
	ttl, err := client.Do(t.Context(), client.B().Pttl().Key(redis.DefaultRedisPrefix+"key").Build()).AsInt64()
	require.NoError(t, err)
	require.Greater(t, ttl, int64(60_000))
	require.LessOrEqual(t, ttl, int64(90_000))
	_, isSweeper := any(value).(store.Sweeper)
	require.False(t, isSweeper)
}

func TestStore_WrongTypeKeyFails(t *testing.T) {
	t.Parallel()
	client := redistest.Start(t)
	store, err := redis.New(client, "wrong:")
	require.NoError(t, err)
	require.NoError(t, client.Do(t.Context(), client.B().Set().Key("wrong:key").Value("plain").Build()).Error())
	_, err = store.Claim(t.Context(), "key", "fingerprint-a", time.Minute)
	require.Error(t, err)
}

func TestNew_NilClient(t *testing.T) {
	t.Parallel()
	store, err := redis.New(nil, "")
	require.Error(t, err)
	require.Nil(t, store)
}
