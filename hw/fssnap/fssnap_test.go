// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package fssnap_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/hw/fssnap"
)

func TestZFSVarNotNil(t *testing.T) {
	t.Parallel()
	// Just verifies the package vars are accessible; no live ZFS required.
	require.NotNil(t, &fssnap.ZFS)
	require.NotNil(t, &fssnap.Btrfs)
}
