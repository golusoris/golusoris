// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ebpf

import (
	"errors"
	"testing"

	"github.com/cilium/ebpf"
	"github.com/stretchr/testify/require"
)

func TestRegistry_Register(t *testing.T) {
	t.Parallel()
	errSecond := errors.New("second")
	reg := &Registry{}
	reg.Register("first", func(*ebpf.Collection) error { return nil })
	reg.Register("second", func(*ebpf.Collection) error { return errSecond })

	require.Len(t, reg.loaders, 2)
	require.Equal(t, "first", reg.loaders[0].name)
	require.Equal(t, "second", reg.loaders[1].name)
	require.NoError(t, reg.loaders[0].fn(nil))
	require.ErrorIs(t, reg.loaders[1].fn(nil), errSecond)
}
