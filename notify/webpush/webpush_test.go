package webpush_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/notify"
	"github.com/golusoris/golusoris/notify/webpush"
)

// stubSubscription fakes a browser PushSubscription. The keys don't
// need to be real P-256 because webpush-go only performs the ECDH on
// the client public key at send-time; the mock push service accepts any
// POST and we never decrypt.
// For encryption to succeed, though, P256dh must be a valid P-256
// point. We generate a real keypair and throw away the private half.
func stubSubscription(t *testing.T, endpoint string) string {
	t.Helper()
	// Generate real P-256 + auth bytes to satisfy webpush-go's
	// encryption path.
	priv, pub, err := webpush.NewVAPIDKeys()
	require.NoError(t, err)
	_ = priv // only pub matters for the client-side key

	// Pretend auth secret (16 random bytes base64'd).
	auth := "DGv6ra1nlYgDCS1FRnbzlw"
	s, err := webpush.EncodeSubscription(endpoint, pub, auth)
	require.NoError(t, err)
	return s
}

func TestSender_Send(t *testing.T) {
	t.Parallel()

	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.NotEmpty(t, r.Header.Get("Authorization"))
		require.Contains(t, r.Header.Get("Authorization"), "vapid")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)

	priv, pub, err := webpush.NewVAPIDKeys()
	require.NoError(t, err)

	s, err := webpush.NewSender(webpush.Options{
		VAPIDPublicKey:  pub,
		VAPIDPrivateKey: priv,
		Subject:         "mailto:ops@example.com",
	})
	require.NoError(t, err)
	require.Equal(t, "webpush", s.Name())

	require.NoError(t, s.Send(context.Background(), notify.Message{
		Body: `{"title":"Hi"}`,
		Metadata: map[string]string{
			"subscription": stubSubscription(t, srv.URL),
		},
	}))
	require.NotEmpty(t, gotBody, "push service received no body")
}

func TestSender_RejectsMissingSubscription(t *testing.T) {
	t.Parallel()
	priv, pub, _ := webpush.NewVAPIDKeys()
	s, _ := webpush.NewSender(webpush.Options{VAPIDPublicKey: pub, VAPIDPrivateKey: priv})
	require.Error(t, s.Send(context.Background(), notify.Message{Body: "x"}))
}

func TestSender_RejectsEmptyBody(t *testing.T) {
	t.Parallel()
	priv, pub, _ := webpush.NewVAPIDKeys()
	s, _ := webpush.NewSender(webpush.Options{VAPIDPublicKey: pub, VAPIDPrivateKey: priv})
	require.Error(t, s.Send(context.Background(), notify.Message{
		Metadata: map[string]string{"subscription": `{"endpoint":"http://x","keys":{}}`},
	}))
}

func TestSender_RejectsMissingKeys(t *testing.T) {
	t.Parallel()
	_, err := webpush.NewSender(webpush.Options{VAPIDPrivateKey: "x"})
	require.Error(t, err)
	_, err = webpush.NewSender(webpush.Options{VAPIDPublicKey: "x"})
	require.Error(t, err)
}

func TestEncodeSubscription(t *testing.T) {
	t.Parallel()
	raw, err := webpush.EncodeSubscription("http://push/abc", "pub", "auth")
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &m))
	require.Equal(t, "http://push/abc", m["endpoint"])
}
