package wol_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/net/wol"
)

func TestMagicPacket_valid(t *testing.T) {
	t.Parallel()
	pkt, err := wol.MagicPacket("aa:bb:cc:dd:ee:ff")
	require.NoError(t, err)
	require.Len(t, pkt, 102)
	// header: 6 × 0xFF
	for i := range 6 {
		require.Equal(t, byte(0xFF), pkt[i])
	}
	// first repetition of MAC
	require.Equal(t, []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}, pkt[6:12])
}

func TestMagicPacket_hyphen(t *testing.T) {
	t.Parallel()
	pkt, err := wol.MagicPacket("AA-BB-CC-DD-EE-FF")
	require.NoError(t, err)
	require.Len(t, pkt, 102)
}

func TestMagicPacket_invalid(t *testing.T) {
	t.Parallel()
	_, err := wol.MagicPacket("not-a-mac")
	require.Error(t, err)
}
