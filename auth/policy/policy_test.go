// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package policy_test

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
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
	sum := sha1.Sum([]byte(pw))
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
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(c.body)),
		Header:     make(http.Header),
	}, nil
}

// errClose is returned by failCloser.Close to exercise the deferred
// close-error path of the HIBP lookup.
var errClose = errors.New("close failed")

// failCloser is a response body whose Close always fails.
type failCloser struct{ io.Reader }

func (failCloser) Close() error { return errClose }

// failCloseTransport answers every request with status and body served
// behind a failCloser.
type failCloseTransport struct {
	status int
	body   string
}

func (c failCloseTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: c.status,
		Body:       failCloser{strings.NewReader(c.body)},
		Header:     make(http.Header),
	}, nil
}

func TestPolicy_HIBPCloseErrorSurfaces(t *testing.T) {
	t.Parallel()

	p := policy.New(policy.Options{
		MinLength:  4,
		MinScore:   3,
		CheckHIBP:  true,
		HTTPClient: &http.Client{Transport: failCloseTransport{status: http.StatusOK, body: "DEADBEEFDEADBEEFDEADBEEFDEADBEEFDEAD:7\r\n"}},
	})
	err := p.Validate(context.Background(), "Tr0ub4dor&3-purple-monkey")
	require.ErrorIs(t, err, errClose)
	require.ErrorContains(t, err, "close hibp response body")
}

func TestPolicy_HIBPPrimaryErrorWinsOverClose(t *testing.T) {
	t.Parallel()

	p := policy.New(policy.Options{
		MinLength:  4,
		MinScore:   3,
		CheckHIBP:  true,
		HTTPClient: &http.Client{Transport: failCloseTransport{status: http.StatusServiceUnavailable}},
	})
	err := p.Validate(context.Background(), "Tr0ub4dor&3-purple-monkey")
	require.ErrorContains(t, err, "hibp: status 503")
	require.NotErrorIs(t, err, errClose)
}
