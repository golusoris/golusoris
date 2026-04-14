package passkeys_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/auth/passkeys"
)

func TestNew_Validates(t *testing.T) {
	t.Parallel()

	_, err := passkeys.New(passkeys.Options{
		RPID:      "example.com",
		RPName:    "Example",
		RPOrigins: []string{"https://example.com"},
	})
	require.NoError(t, err)
}

func TestNew_BadConfig(t *testing.T) {
	t.Parallel()
	_, err := passkeys.New(passkeys.Options{}) // missing RPID
	require.Error(t, err)
}

func TestProvisionTOTP_RoundTrip(t *testing.T) {
	t.Parallel()

	k, err := passkeys.ProvisionTOTP("Example", "alice@example.com")
	require.NoError(t, err)
	require.NotEmpty(t, k.Secret())
	require.Contains(t, k.URL(), "otpauth://")
}

func TestVerifyTOTP_RejectsBadCode(t *testing.T) {
	t.Parallel()

	k, err := passkeys.ProvisionTOTP("Example", "alice@example.com")
	require.NoError(t, err)

	require.Error(t, passkeys.VerifyTOTP(k.Secret(), "000000"))
}

func TestVerifyTOTP_RejectsBadSecret(t *testing.T) {
	t.Parallel()
	require.Error(t, passkeys.VerifyTOTP("not-base32!!", "123456"))
}
