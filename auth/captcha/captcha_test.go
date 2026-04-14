package captcha_test

import (
	"context"
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
	if i := strings.Index(t, "://"); i >= 0 {
		req.URL.Scheme = t[:i]
		req.URL.Host = t[i+3:]
	} else {
		req.URL.Host = t
	}
	return r.base.RoundTrip(req)
}
