// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package httpcache

import (
	"testing"

	"github.com/bartventer/httpcache/store"
	"github.com/bartventer/httpcache/store/driver"
	"github.com/stretchr/testify/require"
)

func TestDriverInstancesAndClose(t *testing.T) {
	t.Parallel()
	a, _ := testCache(t, DefaultOptions())
	b, _ := testCache(t, DefaultOptions())
	require.NotEqual(t, a.id, b.id)
	conn, err := store.Open(driverScheme + "://" + a.id)
	require.NoError(t, err)
	_, err = conn.Get("missing")
	require.ErrorIs(t, err, driver.ErrNotExist)
	require.NoError(t, conn.Delete("missing"))
	require.NoError(t, conn.Set("key", []byte("value")))
	got, err := conn.Get("key")
	require.NoError(t, err)
	require.Equal(t, []byte("value"), got)
	require.NoError(t, conn.Delete("key"))
	_, err = conn.Get("key")
	require.ErrorIs(t, err, driver.ErrNotExist)
	require.NoError(t, a.Close())
	require.NoError(t, a.Close())
	_, err = store.Open(driverScheme + "://" + a.id)
	require.Error(t, err)
	_, err = store.Open(driverScheme + "://" + b.id)
	require.NoError(t, err)
	_, err = store.Open(driverScheme + "://unknown")
	require.Error(t, err)
}
