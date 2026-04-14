package smtpserver_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/net/smtpserver"
)

func TestDefaultConfig(t *testing.T) {
	t.Parallel()
	d := smtpserver.DefaultConfig()
	require.Equal(t, ":2525", d.Addr)
	require.Equal(t, "localhost", d.Domain)
	require.Equal(t, int64(10<<20), d.MaxMessageBytes)
	require.Equal(t, 50, d.MaxRecipients)
	require.Equal(t, 60*time.Second, d.ReadTimeout)
}

func TestHandlerBackend_NewSession(t *testing.T) {
	t.Parallel()
	called := false
	b := smtpserver.NewHandlerBackend(func(env smtpserver.Envelope) error {
		called = true
		require.Equal(t, "sender@example.com", env.From)
		return nil
	})
	require.NotNil(t, b)
	_ = called
}
