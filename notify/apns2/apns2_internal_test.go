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
	"net/http"
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
