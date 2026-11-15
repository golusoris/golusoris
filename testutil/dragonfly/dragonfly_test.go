// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package dragonfly

import (
	"context"
	"testing"
	"time"

	"github.com/redis/rueidis"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/internal/testimages"
)

func TestOptionsImage(t *testing.T) {
	t.Parallel()
	for _, image := range []string{"", testimages.Dragonfly, "dragonfly:latest", "dragonfly:v2@sha256:bad"} {
		t.Run(image, func(t *testing.T) {
			t.Parallel()
			actual, err := (Options{Image: image}).image()
			if image == "" || image == testimages.Dragonfly {
				require.NoError(t, err)
				require.Equal(t, testimages.Dragonfly, actual)
			} else {
				require.Error(t, err)
				require.Empty(t, actual)
			}
		})
	}
}

func TestClientSideCaching_Invalidation(t *testing.T) {
	t.Parallel()
	addr := Addr(t)
	client, err := rueidis.NewClient(rueidis.ClientOption{InitAddress: []string{addr}})
	require.NoError(t, err)
	t.Cleanup(client.Close)
	writer, err := rueidis.NewClient(rueidis.ClientOption{InitAddress: []string{addr}, DisableCache: true})
	require.NoError(t, err)
	t.Cleanup(writer.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, writer.Do(ctx, writer.B().Set().Key("tracking").Value("v1").Build()).Error())
	read := func() rueidis.RedisResult {
		return client.DoCache(ctx, client.B().Get().Key("tracking").Cache(), time.Minute)
	}
	first := read()
	value, err := first.ToString()
	require.NoError(t, err)
	require.Equal(t, "v1", value)
	require.False(t, first.IsCacheHit())
	require.True(t, read().IsCacheHit())
	require.NoError(t, writer.Do(ctx, writer.B().Set().Key("tracking").Value("v2").Build()).Error())
	invalidated := false
	for range 50 {
		result := read()
		value, readErr := result.ToString()
		require.NoError(t, readErr)
		if value == "v2" && !result.IsCacheHit() {
			invalidated = true
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	require.True(t, invalidated, "write must invalidate the tracked entry")
	require.True(t, read().IsCacheHit())
}
