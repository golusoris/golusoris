package dnsserver_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/net/dnsserver"
)

func TestDefaultConfig(t *testing.T) {
	t.Parallel()
	d := dnsserver.DefaultConfig()
	require.Equal(t, ":5353", d.Addr)
	require.Equal(t, 4096, d.UDPSize)
}
