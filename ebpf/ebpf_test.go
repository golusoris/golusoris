package ebpf_test

import (
	"testing"

	ciliumebpf "github.com/cilium/ebpf"
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

func TestRegistry_Register(t *testing.T) {
	t.Parallel()
	reg := &oureBPF.Registry{}
	// Register a no-op loader — verify it doesn't panic.
	reg.Register("noop", func(_ *ciliumebpf.Collection) error { return nil })
}
