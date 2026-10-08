// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package captcha_test

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/auth/captcha"
)

func TestVerifier_Success(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		require.Equal(t, "tok-ok", r.Form.Get("response"))
		require.Equal(t, "secret", r.Form.Get("secret"))
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	t.Cleanup(srv.Close)

	// Re-point Turnstile at the test server via a URL-rewriting RoundTripper.
	client := &http.Client{Transport: rewriteTransport{base: srv.Client().Transport, target: srv.URL}}
	v := captcha.NewTurnstile("secret", client)

	require.NoError(t, v.Verify(context.Background(), "tok-ok", ""))
}

func TestVerifier_Failure(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"error-codes":["invalid-input-response"]}`))
	}))
	t.Cleanup(srv.Close)

	client := &http.Client{Transport: rewriteTransport{base: http.DefaultTransport, target: srv.URL}}
	v := captcha.NewHCaptcha("secret", client)

	err := v.Verify(context.Background(), "tok", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid-input-response")
}

func TestVerifierRejectsSuccessfulBodyFromFailedHTTPResponse(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	t.Cleanup(srv.Close)

	client := &http.Client{Transport: rewriteTransport{base: http.DefaultTransport, target: srv.URL}}
	v := captcha.NewTurnstile("secret", client)

	err := v.Verify(context.Background(), "tok", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "HTTP status 502")
}

func TestRecaptchaV2RejectsV3Response(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{
		"score and action": `{"success":true,"score":0.9,"action":"login"}`,
		"null score":       `{"success":true,"score":null}`,
		"null action":      `{"success":true,"action":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			verifier := captcha.NewRecaptcha("secret", responseClient{body: body})
			err := verifier.Verify(context.Background(), "tok", "")
			require.Error(t, err)
			require.Contains(t, err.Error(), "NewRecaptchaV3")
		})
	}
}

func TestRecaptchaV3Policy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "accepted at threshold", body: `{"success":true,"score":0.7,"action":"account/login"}`},
		{name: "low score", body: `{"success":true,"score":0.69,"action":"account/login"}`, wantErr: "score below threshold"},
		{name: "wrong action", body: `{"success":true,"score":1,"action":"account/signup"}`, wantErr: "action mismatch"},
		{name: "missing score", body: `{"success":true,"action":"account/login"}`, wantErr: "missing score or action"},
		{name: "missing action", body: `{"success":true,"score":1}`, wantErr: "missing score or action"},
		{name: "null score", body: `{"success":true,"score":null,"action":"account/login"}`, wantErr: "invalid score"},
		{name: "score above range", body: `{"success":true,"score":1.1,"action":"account/login"}`, wantErr: "invalid score"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			verifier, err := captcha.NewRecaptchaV3("secret", responseClient{body: test.body}, 0.7, "account/login")
			require.NoError(t, err)
			err = verifier.Verify(context.Background(), "tok", "")
			if test.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestNewRecaptchaV3AcceptsPolicyBoundaries(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		score  float64
		action string
	}{
		{name: "zero score", score: 0, action: "login"},
		{name: "one score", score: 1, action: "ACCOUNT/login_2"},
		{name: "maximum action", score: 0.5, action: strings.Repeat("a", 256)},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			verifier, err := captcha.NewRecaptchaV3("secret", responseClient{}, test.score, test.action)
			require.NoError(t, err)
			require.NotNil(t, verifier)
		})
	}
}

func TestVerifierRejectsOversizedResponse(t *testing.T) {
	t.Parallel()

	verifier := captcha.NewTurnstile("secret", responseClient{
		body: `{"success":true}` + strings.Repeat(" ", 1<<16),
	})
	err := verifier.Verify(context.Background(), "tok", "")
	require.ErrorContains(t, err, "response body exceeds")
}

func TestNewRecaptchaV3RejectsInvalidPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		score  float64
		action string
	}{
		{name: "negative score", score: -0.1, action: "login"},
		{name: "score above one", score: 1.1, action: "login"},
		{name: "NaN score", score: math.NaN(), action: "login"},
		{name: "infinite score", score: math.Inf(1), action: "login"},
		{name: "empty action", score: 0.5},
		{name: "blank action", score: 0.5, action: " "},
		{name: "invalid action character", score: 0.5, action: "account-login"},
		{name: "oversized action", score: 0.5, action: strings.Repeat("a", 257)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			verifier, err := captcha.NewRecaptchaV3("secret", responseClient{}, test.score, test.action)
			require.Error(t, err)
			require.Nil(t, verifier)
		})
	}
}

func TestVerifier_MissingToken(t *testing.T) {
	t.Parallel()
	v := captcha.NewRecaptcha("secret", nil)
	require.Error(t, v.Verify(context.Background(), "", ""))
}

// rewriteTransport rewrites the request URL to a fixed target.
type rewriteTransport struct {
	base   http.RoundTripper
	target string
}

func (r rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Replace scheme+host with target.
	t := strings.TrimSuffix(r.target, "/")
	req.URL.Scheme = "http"
	if before, after, ok := strings.Cut(t, "://"); ok {
		req.URL.Scheme = before
		req.URL.Host = after
	} else {
		req.URL.Host = t
	}
	return r.base.RoundTrip(req)
}

// errClose is returned by failCloser.Close to exercise the deferred
// close-error path.
var errClose = errors.New("close failed")

// failCloser is a response body whose Close always fails.
type failCloser struct{ io.Reader }

func (failCloser) Close() error { return errClose }

// cannedClient satisfies captcha.HTTPClient with a fixed JSON body served
// behind a failCloser.
type cannedClient struct{ body string }

func (c cannedClient) Do(_ *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       failCloser{strings.NewReader(c.body)},
		Header:     make(http.Header),
	}, nil
}

type responseClient struct {
	body string
}

func (c responseClient) Do(_ *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(c.body)),
		Header:     make(http.Header),
	}, nil
}

func TestVerifier_CloseErrorSurfaces(t *testing.T) {
	t.Parallel()
	v := captcha.NewTurnstile("secret", cannedClient{body: `{"success":true}`})

	err := v.Verify(context.Background(), "tok", "")
	require.ErrorIs(t, err, errClose)
	require.ErrorContains(t, err, "close response body")
}

func TestVerifier_PrimaryErrorWinsOverClose(t *testing.T) {
	t.Parallel()
	v := captcha.NewTurnstile("secret", cannedClient{body: `{"success":false,"error-codes":["invalid-input-response"]}`})

	err := v.Verify(context.Background(), "tok", "")
	require.ErrorContains(t, err, "invalid-input-response")
	require.NotErrorIs(t, err, errClose)
}
