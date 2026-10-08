// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package twotier

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/cache/memory"
	"github.com/golusoris/golusoris/realtime/pubsub/redis"
	redistest "github.com/golusoris/golusoris/testutil/redis"
)

// TestRedisL2_DelPrefix_RealRedis is the positive case: DelPrefix removes
// every key matching "<prefix>*" across multiple SCAN pages (scanCount=256,
// so 300 keys forces at least two rounds) and leaves non-matching keys
// untouched.
func TestRedisL2_DelPrefix_RealRedis(t *testing.T) {
	t.Parallel()
	client := redistest.Start(t)
	l2 := redisL2{client: client}
	ctx := context.Background()

	const matching = scanCount + 44 // forces >1 SCAN round
	for i := range matching {
		key := fmt.Sprintf("victim:%d", i)
		require.NoError(t, client.Do(ctx, client.B().Set().Key(key).Value("v").Build()).Error())
	}
	require.NoError(t, client.Do(ctx, client.B().Set().Key("keep:1").Value("v").Build()).Error())

	require.NoError(t, l2.DelPrefix(ctx, "victim:"))

	for i := range matching {
		key := fmt.Sprintf("victim:%d", i)
		n, err := client.Do(ctx, client.B().Exists().Key(key).Build()).ToInt64()
		require.NoError(t, err)
		require.Zerof(t, n, "key %q should have been deleted", key)
	}
	n, err := client.Do(ctx, client.B().Exists().Key("keep:1").Build()).ToInt64()
	require.NoError(t, err)
	require.EqualValues(t, 1, n, "non-matching key must survive DelPrefix")
}

// TestRedisL2_DelPrefix_EmptyPrefixRejected is the negative case: an empty
// prefix is refused before any Redis round-trip, guarding against a
// scan-the-whole-keyspace accident.
func TestRedisL2_DelPrefix_EmptyPrefixRejected(t *testing.T) {
	t.Parallel()
	client := redistest.Start(t)
	l2 := redisL2{client: client}

	err := l2.DelPrefix(context.Background(), "")
	require.Error(t, err)
}

// TestRedisL2_DelPrefix_NoMatchesIsNoop is the boundary case: a prefix that
// matches nothing completes cleanly after the first empty SCAN round.
func TestRedisL2_DelPrefix_NoMatchesIsNoop(t *testing.T) {
	t.Parallel()
	client := redistest.Start(t)
	l2 := redisL2{client: client}

	require.NoError(t, l2.DelPrefix(context.Background(), "nothing-has-this-prefix:"))
}

// TestInvalidation_RealRedisAcrossReplicas is the #623 acceptance: two
// replicas share a Redis L2 and the Redis pub/sub bus; a Delete on one
// evicts the other's L1 within one second once both subscriptions are up.
func TestInvalidation_RealRedisAcrossReplicas(t *testing.T) {
	t.Parallel()
	client := redistest.Start(t)
	newReplica := func() *Typed[int] {
		l1, err := memory.NewForTest(100, 0)
		require.NoError(t, err)
		broadcaster, err := NewBusBroadcaster(redis.New(client, nil), "test.invalidate", nil)
		require.NoError(t, err)
		opts := Options{L2: L2Redis, L1TTL: time.Hour, L2TTL: time.Minute, Invalidation: InvalidationOptions{Enabled: true}}
		tt, err := New(l1, opts, slog.New(slog.DiscardHandler), WithRedis(client), WithBroadcaster(broadcaster))
		require.NoError(t, err)
		t.Cleanup(tt.Listen())
		return NewTyped[int](tt, "inv")
	}
	a, b := newReplica(), newReplica()
	ctx := context.Background()
	fromL2 := func(context.Context) (int, error) { return -1, nil }

	// SUBSCRIBE starts asynchronously and a notice sent before it is lost (the
	// documented at-most-once bound), so re-send until b observes a's write.
	require.NoError(t, a.Set(ctx, "k", 1))
	got, err := b.Get(ctx, "k", fromL2)
	require.NoError(t, err)
	require.Equal(t, 1, got)
	require.Eventually(t, func() bool {
		if setErr := a.Set(ctx, "k", 2); setErr != nil {
			return false
		}
		value, getErr := b.Get(ctx, "k", fromL2)
		return getErr == nil && value == 2
	}, 30*time.Second, 100*time.Millisecond)

	require.NoError(t, a.Delete(ctx, "k"))
	require.Eventually(t, func() bool {
		value, getErr := b.Get(ctx, "k", func(context.Context) (int, error) { return 3, nil })
		return getErr == nil && value == 3
	}, time.Second, 10*time.Millisecond, "peer L1 evicted within one second of the Delete")
}
