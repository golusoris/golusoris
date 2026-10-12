// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ebpf_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	oureBPF "github.com/golusoris/golusoris/ebpf"
)

func TestBytesProvider(t *testing.T) {
	t.Parallel()
	data := []byte("fake elf data")
	p := oureBPF.BytesProvider(data)
	got, err := p()
	require.NoError(t, err)
	require.Equal(t, data, got)
}
