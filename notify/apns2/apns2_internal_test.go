// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package apns2

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNewSender_DefaultTransportSpeaksH2 pins the default client shape: APNs
// is HTTP/2-only, so the transport must advertise h2 (HTTP/1.1 kept as ALPN
// fallback) through Transport.Protocols, the replacement for x/net's
// deprecated http2.ConfigureTransport.
func TestNewSender_DefaultTransportSpeaksH2(t *testing.T) {
	t.Parallel()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	p8 := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	s, err := NewSender(Options{KeyID: "ABC1234567", TeamID: "TEAM123456", Topic: "com.example.app", P8Key: p8})
	require.NoError(t, err)

	tr, ok := s.hc.Transport.(*http.Transport)
	require.True(t, ok, "default client must use *http.Transport")
	require.NotNil(t, tr.Protocols)
	require.True(t, tr.Protocols.HTTP2(), "APNs requires HTTP/2")
	require.True(t, tr.Protocols.HTTP1(), "HTTP/1.1 stays as ALPN fallback")
	require.False(t, tr.Protocols.UnencryptedHTTP2(), "h2c must not be offered")
	require.GreaterOrEqual(t, tr.TLSClientConfig.MinVersion, uint16(tls.VersionTLS12))
}

// closeErrRoundTripper is a fake http.RoundTripper that returns a synthetic
// response whose Body.Close reports closeErr, so do's error handling can be
// exercised without a real APNs server.
type closeErrRoundTripper struct {
	statusCode int
	closeErr   error
}

func (rt closeErrRoundTripper) RoundTrip(_ *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: rt.statusCode,
		Body:       closeErrBody{Reader: strings.NewReader(""), err: rt.closeErr},
		Header:     make(http.Header),
	}, nil
}

// closeErrBody wraps a Reader and reports err from Close, regardless of err's
// nilness — used to make an http.Response.Body whose Close either succeeds or
// fails on demand.
type closeErrBody struct {
	io.Reader
	err error
}

func (b closeErrBody) Close() error { return b.err }

// TestDo_OKWithNoCloseError is the positive case (HISS-07): a 200 response
// whose body closes cleanly returns a nil error.
func TestDo_OKWithNoCloseError(t *testing.T) {
	t.Parallel()
	s := &Sender{hc: &http.Client{Transport: closeErrRoundTripper{statusCode: http.StatusOK}}}
	req, err := http.NewRequest(http.MethodPost, "https://example.invalid/3/device/abc", nil)
	require.NoError(t, err)
	require.NoError(t, s.do(req, "abc"))
}

// TestDo_SurfacesBodyCloseError is the negative case (HISS-07): do used to
// discard the response body's Close error via `_ = resp.Body.Close()`; it
// must now surface as the returned error when the primary call otherwise
// succeeded.
func TestDo_SurfacesBodyCloseError(t *testing.T) {
	t.Parallel()
	closeErr := errors.New("close boom")
	s := &Sender{hc: &http.Client{Transport: closeErrRoundTripper{statusCode: http.StatusOK, closeErr: closeErr}}}
	req, err := http.NewRequest(http.MethodPost, "https://example.invalid/3/device/abc", nil)
	require.NoError(t, err)
	err = s.do(req, "abc")
	require.Error(t, err)
	require.ErrorIs(t, err, closeErr)
	require.Contains(t, err.Error(), "close response body")
}

// TestDo_StatusErrorTakesPrecedenceOverCloseError is the boundary case: when
// both the APNs status and the body close fail, the status error must win
// (CloseInto never overwrites an already-set error), matching the primary
// failure a caller cares about.
func TestDo_StatusErrorTakesPrecedenceOverCloseError(t *testing.T) {
	t.Parallel()
	closeErr := errors.New("close boom")
	s := &Sender{hc: &http.Client{Transport: closeErrRoundTripper{
		statusCode: http.StatusInternalServerError,
		closeErr:   closeErr,
	}}}
	req, err := http.NewRequest(http.MethodPost, "https://example.invalid/3/device/abc", nil)
	require.NoError(t, err)
	err = s.do(req, "abc")
	require.Error(t, err)
	require.Contains(t, err.Error(), "status 500")
	require.NotErrorIs(t, err, closeErr)
}
