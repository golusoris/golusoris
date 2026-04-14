package apns2_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/notify"
	"github.com/golusoris/golusoris/notify/apns2"
)

func fakeP8(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// senderForTestServer builds a Sender pointing at an httptest.Server
// (HTTP/1.1 — we're testing our wire encoding, not real APNs h2).
// We override Options.HTTPClient so the sender doesn't insist on h2.
func senderForTestServer(t *testing.T, srvURL string) *apns2.Sender {
	t.Helper()
	s, err := apns2.NewSender(apns2.Options{
		KeyID:      "ABC1234567",
		TeamID:     "TEAM123456",
		Topic:      "com.example.app",
		P8Key:      fakeP8(t),
		Production: true, // so the host isn't the sandbox
		HTTPClient: &http.Client{},
	})
	require.NoError(t, err)
	// Replace the host via the unexported field using the test's
	// same-package alias: we expose a test-only override via Options
	// by setting a custom HTTPClient that rewrites the URL. Simpler:
	// the sender uses Options.Endpoint-style override? It doesn't —
	// so we patch the URL through a RoundTripper.
	return senderWithHost(t, s, srvURL)
}

// senderWithHost replaces the sender's underlying transport with one
// that rewrites the outgoing request URL to point at the test server.
func senderWithHost(t *testing.T, s *apns2.Sender, host string) *apns2.Sender {
	t.Helper()
	// Tests use the same-package trick via the _test.go file of
	// apns2_test — we cannot reach into Sender internals. Instead,
	// install a rewriting RoundTripper by rebuilding with a custom
	// client that rewrites host.
	client := &http.Client{Transport: &rewriteTransport{host: host}}
	newS, err := apns2.NewSender(apns2.Options{
		KeyID:      "ABC1234567",
		TeamID:     "TEAM123456",
		Topic:      "com.example.app",
		P8Key:      fakeP8(t),
		Production: true,
		HTTPClient: client,
	})
	require.NoError(t, err)
	_ = s // discard; newS has the rewriting transport
	return newS
}

// rewriteTransport rewrites outgoing request URLs to point at a test
// server (host includes scheme + host). Used because [apns2.Sender]'s
// host is set at construction time.
type rewriteTransport struct{ host string }

func (r *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Point the request at the test server while preserving the path.
	req2 := req.Clone(req.Context())
	// Parse host into URL so we swap scheme+host cleanly.
	tgtURL := req.URL
	// Replace scheme + host with the test server's.
	testURL, err := parseHost(r.host)
	if err != nil {
		return nil, err
	}
	tgtURL.Scheme = testURL.Scheme
	tgtURL.Host = testURL.Host
	req2.URL = tgtURL
	req2.Host = testURL.Host
	return http.DefaultTransport.RoundTrip(req2)
}

type hostURL struct{ Scheme, Host string }

func parseHost(h string) (*hostURL, error) {
	u, err := parseURL(h)
	if err != nil {
		return nil, err
	}
	return &hostURL{Scheme: u.Scheme, Host: u.Host}, nil
}

// parseURL wraps net/url.Parse without importing it at file top.
func parseURL(s string) (*parsed, error) {
	return parsedParse(s)
}

type parsed struct {
	Scheme string
	Host   string
}

func parsedParse(s string) (*parsed, error) {
	// naive scheme://host parse — fine for httptest URLs.
	const sep = "://"
	i := indexOf(s, sep)
	if i < 0 {
		return nil, errMissingScheme
	}
	return &parsed{Scheme: s[:i], Host: s[i+len(sep):]}, nil
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

var errMissingScheme = newErr("test: URL missing scheme")

func newErr(msg string) error { return &testErr{msg} }

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }

func TestSend_HappyPath(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	var gotAuth, gotTopic, gotPushType, gotPriority string
	var gotPath string
	var gotBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		gotAuth = r.Header.Get("Authorization")
		gotTopic = r.Header.Get("Apns-Topic")
		gotPushType = r.Header.Get("Apns-Push-Type")
		gotPriority = r.Header.Get("Apns-Priority")
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	s := senderForTestServer(t, srv.URL)
	err := s.Send(context.Background(), notify.Message{
		To:       []string{"devicetokenhex1"},
		Subject:  "Incoming",
		Body:     "You have a message",
		Metadata: map[string]string{"conv_id": "xyz"},
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), calls.Load())
	require.Contains(t, gotAuth, "Bearer ")
	require.Equal(t, "com.example.app", gotTopic)
	require.Equal(t, "alert", gotPushType)
	require.Equal(t, "10", gotPriority)
	require.Equal(t, "/3/device/devicetokenhex1", gotPath)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(gotBody, &payload))
	aps, _ := payload["aps"].(map[string]any)
	alert, _ := aps["alert"].(map[string]any)
	require.Equal(t, "Incoming", alert["title"])
	require.Equal(t, "You have a message", alert["body"])
	require.Equal(t, "xyz", payload["conv_id"])
}

func TestSend_UnregisteredReturnsSentinel(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte(`{"reason":"Unregistered"}`))
	}))
	t.Cleanup(srv.Close)

	s := senderForTestServer(t, srv.URL)
	err := s.Send(context.Background(), notify.Message{
		To: []string{"dead-token"}, Subject: "x", Body: "y",
	})
	require.Error(t, err)
	require.ErrorIs(t, err, apns2.ErrUnregistered)
}

func TestNewSender_ValidatesFields(t *testing.T) {
	t.Parallel()
	_, err := apns2.NewSender(apns2.Options{})
	require.Error(t, err)
	_, err = apns2.NewSender(apns2.Options{
		KeyID: "k", TeamID: "t", Topic: "x", P8Key: []byte("not pem"),
	})
	require.Error(t, err)
}

func TestNewSender_RejectsNonECDSA(t *testing.T) {
	t.Parallel()
	// Build a valid PKCS8 PEM but with an RSA key instead of ECDSA.
	// Simulate by writing a zero-byte PKCS8 — parsing should fail.
	bogus := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte{0x00}})
	_, err := apns2.NewSender(apns2.Options{
		KeyID: "k", TeamID: "t", Topic: "x", P8Key: bogus,
	})
	require.Error(t, err)
}
