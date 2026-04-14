package client_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gqlclient "github.com/golusoris/golusoris/graphql/client"
)

func TestDefaultConfig(t *testing.T) {
	t.Parallel()
	d := gqlclient.DefaultConfig()
	require.Equal(t, 30*time.Second, d.Timeout)
	require.Empty(t, d.Endpoint)
	require.False(t, d.WebSocket)
}
