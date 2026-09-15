// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package twotier

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

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
