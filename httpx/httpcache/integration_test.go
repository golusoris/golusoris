// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package httpcache

import (
	"log/slog"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/bartventer/httpcache"
	"github.com/redis/rueidis"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/cache/memory"
	"github.com/golusoris/golusoris/cache/twotier"
	"github.com/golusoris/golusoris/testutil/dragonfly"
	"github.com/golusoris/golusoris/testutil/redis"
)

func TestCacheSharedL2_RealRedis(t *testing.T) {
	t.Parallel()
	testSharedL2(t, redis.Start(t))
}

func TestCacheSharedL2_RealDragonfly(t *testing.T) {
	t.Parallel()
	testSharedL2(t, dragonfly.Start(t))
}

// testSharedL2 checks that one replica serves another replica's stored response and that L2 holds no secret.
func testSharedL2(t *testing.T, client rueidis.Client) {
	t.Helper()
	const secret = "integration-secret-value"
	var calls atomic.Int32
	upstream := transportFunc(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		require.Equal(t, secret, req.URL.Query().Get("api_key"))
		return response(200, "metadata", "Cache-Control", "max-age=60"), nil
	})
	newReplica := func() *Cache {
		l1, err := memory.NewForTest(100, 0)
		require.NoError(t, err)
		opts := DefaultOptions()
		storeOpts := twotier.Options{L2: twotier.L2Redis, L1TTL: opts.Store.L1TTL, L2TTL: opts.Store.Retention}
		tt, err := twotier.New(l1, storeOpts, slog.New(slog.DiscardHandler), twotier.WithRedis(client))
		require.NoError(t, err)
		c, err := New(opts, tt, slog.New(slog.DiscardHandler))
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, c.Close()) })
		return c
	}
	target := "http://provider.test/series/1?api_key=" + secret
	_, body := readResponse(t, newReplica().Wrap(upstream), request(t, target))
	require.Equal(t, "metadata", body)
	resp, body := readResponse(t, newReplica().Wrap(upstream), request(t, target))
	require.Equal(t, "metadata", body)
	require.Equal(t, "HIT", resp.Header.Get(httpcache.CacheStatusHeader))
	require.EqualValues(t, 1, calls.Load())

	keys, err := client.Do(t.Context(), client.B().Keys().Pattern("*").Build()).AsStrSlice()
	require.NoError(t, err)
	require.NotEmpty(t, keys)
	for _, key := range keys {
		require.NotContains(t, key, secret)
		value, getErr := client.Do(t.Context(), client.B().Get().Key(key).Build()).ToString()
		require.NoError(t, getErr)
		require.NotContains(t, value, secret)
	}
}
