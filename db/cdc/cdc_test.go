package cdc_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/db/cdc"
)

func TestDefaultConfig(t *testing.T) {
	t.Parallel()
	d := cdc.DefaultConfig()
	require.Equal(t, "golusoris", d.Slot)
	require.Equal(t, "golusoris", d.Publication)
	require.Equal(t, 10, d.StandbyHz)
	require.Empty(t, d.DSN)
}

func TestOpConstants(t *testing.T) {
	t.Parallel()
	ops := []cdc.Op{cdc.OpInsert, cdc.OpUpdate, cdc.OpDelete, cdc.OpTruncate}
	seen := map[cdc.Op]struct{}{}
	for _, op := range ops {
		require.NotEmpty(t, op)
		_, dup := seen[op]
		require.False(t, dup, "duplicate Op value: %s", op)
		seen[op] = struct{}{}
	}
}
