// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package redis

import (
	"testing"
	"time"

	"github.com/redis/rueidis"
	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/idempotency/store"
)

type inertClient struct{ rueidis.Client }

func TestStore_ConstructorAndValidation(t *testing.T) {
	t.Parallel()
	var missing *inertClient
	_, err := New(missing, "")
	require.Error(t, err)
	for _, prefix := range []string{"", "custom:"} {
		value, newErr := New(&inertClient{}, prefix)
		require.NoError(t, newErr)
		want := prefix
		if want == "" {
			want = DefaultRedisPrefix
		}
		require.Equal(t, want, value.prefix)
		_, claimErr := value.Claim(t.Context(), "key", "fp", 0)
		require.Error(t, claimErr)
		require.Error(t, value.Commit(t.Context(), "key", "", "fp", store.CachedResponse{}, time.Minute))
	}
}
