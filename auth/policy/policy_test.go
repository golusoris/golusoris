package policy_test

import (
	"context"
	"crypto/sha1" //nolint:gosec // HIBP API uses sha1; test must match.
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/auth/policy"
)

func TestPolicy_RejectsShort(t *testing.T) {
	t.Parallel()
	p := policy.New(policy.Options{MinLength: 12, MinScore: 3})
	require.Error(t, p.Validate(context.Background(), "short"))
}

func TestPolicy_RejectsWeak(t *testing.T) {
	t.Parallel()
	p := policy.New(policy.Options{MinLength: 4, MinScore: 4})
	require.Error(t, p.Validate(context.Background(), "password"))
}

func TestPolicy_AcceptsStrong(t *testing.T) {
	t.Parallel()
	p := policy.New(policy.Options{MinLength: 12, MinScore: 3})
	require.NoError(t, p.Validate(context.Background(), "Tr0ub4dor&3-purple-monkey"))
}

func TestPolicy_HIBPRejectsBreached(t *testing.T) {
	t.Parallel()

	pw := "Tr0ub4dor&3-purple-monkey" // strong enough for zxcvbn ≥3
	sum := sha1.Sum([]byte(pw))        //nolint:gosec
	hash := strings.ToUpper(hex.EncodeToString(sum[:]))
	suffix := hash[5:]

	body := "DEADBEEFDEADBEEFDEADBEEFDEADBEEFDEAD:7\r\n" + suffix + ":42\r\n"

	p := policy.New(policy.Options{
		MinLength:      4,
		MinScore:       3,
		CheckHIBP:      true,
		MaxBreachCount: 0,
		HTTPClient:     &http.Client{Transport: cannedTransport{body: body}},
	})
	err := p.Validate(context.Background(), pw)
	require.Error(t, err)
	require.Contains(t, err.Error(), "42")
}

func TestPolicy_HIBPAllowsClean(t *testing.T) {
	t.Parallel()

	body := "DEADBEEFDEADBEEFDEADBEEFDEADBEEFDEAD:7\r\n"

	p := policy.New(policy.Options{
		MinLength:      4,
		MinScore:       3,
		CheckHIBP:      true,
		MaxBreachCount: 0,
		HTTPClient:     &http.Client{Transport: cannedTransport{body: body}},
	})
	require.NoError(t, p.Validate(context.Background(), "Tr0ub4dor&3-purple-monkey"))
}

type cannedTransport struct{ body string }

func (c cannedTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(c.body)),
		Header:     make(http.Header),
	}, nil
}
