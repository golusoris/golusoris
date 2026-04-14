package fcm_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/notify"
	"github.com/golusoris/golusoris/notify/fcm"
)

// fakeSA builds a ServiceAccount with a freshly-generated RSA private
// key so the JWT-sign path exercises real crypto end-to-end.
func fakeSA(t *testing.T, tokenURI string) fcm.ServiceAccount {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	return fcm.ServiceAccount{
		Type:        "service_account",
		ProjectID:   "my-project",
		ClientEmail: "ci@my-project.iam.gserviceaccount.com",
		PrivateKey:  string(pemBytes),
		TokenURI:    tokenURI,
	}
}

func TestSend_ExchangesTokenAndPosts(t *testing.T) {
	t.Parallel()

	var tokenCalls atomic.Int32
	var sendCalls atomic.Int32
	var gotBody string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			tokenCalls.Add(1)
			require.NoError(t, r.ParseForm())
			require.Equal(t, "urn:ietf:params:oauth:grant-type:jwt-bearer", r.PostForm.Get("grant_type"))
			require.NotEmpty(t, r.PostForm.Get("assertion"))
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"ya29-fake","expires_in":3600,"token_type":"Bearer"}`))

		case strings.HasPrefix(r.URL.Path, "/v1/projects/my-project/messages:send"):
			sendCalls.Add(1)
			require.Equal(t, "Bearer ya29-fake", r.Header.Get("Authorization"))
			body, _ := io.ReadAll(r.Body)
			gotBody = string(body)
			_, _ = w.Write([]byte(`{"name":"projects/my-project/messages/1"}`))

		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	sa := fakeSA(t, srv.URL+"/token")
	s, err := fcm.NewSender(fcm.Options{
		ServiceAccount: &sa,
		Endpoint:       srv.URL,
	})
	require.NoError(t, err)
	require.Equal(t, "fcm", s.Name())

	err = s.Send(context.Background(), notify.Message{
		To:       []string{"device-token-1"},
		Subject:  "New post",
		Body:     "Check it out",
		Metadata: map[string]string{"post_id": "42"},
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), tokenCalls.Load())
	require.Equal(t, int32(1), sendCalls.Load())

	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(gotBody), &got))
	msg, _ := got["message"].(map[string]any)
	require.Equal(t, "device-token-1", msg["token"])
	notif, _ := msg["notification"].(map[string]any)
	require.Equal(t, "New post", notif["title"])
}

func TestSend_ReusesTokenAcrossRecipients(t *testing.T) {
	t.Parallel()

	var tokenCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			tokenCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"t","expires_in":3600}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	sa := fakeSA(t, srv.URL+"/token")
	s, _ := fcm.NewSender(fcm.Options{ServiceAccount: &sa, Endpoint: srv.URL})
	require.NoError(t, s.Send(context.Background(), notify.Message{
		To: []string{"d1", "d2", "d3"}, Subject: "s", Body: "b",
	}))
	// 3 recipients, but token is cached → 1 token fetch.
	require.Equal(t, int32(1), tokenCalls.Load())
}

func TestSend_NoRecipients(t *testing.T) {
	t.Parallel()
	sa := fakeSA(t, "http://unused")
	s, _ := fcm.NewSender(fcm.Options{ServiceAccount: &sa})
	require.Error(t, s.Send(context.Background(), notify.Message{Subject: "x"}))
}

func TestNewSender_RequiresServiceAccount(t *testing.T) {
	t.Parallel()
	_, err := fcm.NewSender(fcm.Options{})
	require.Error(t, err)
}

func TestNewSender_AcceptsJSON(t *testing.T) {
	t.Parallel()
	sa := fakeSA(t, "http://unused")
	b, _ := json.Marshal(sa)
	_, err := fcm.NewSender(fcm.Options{ServiceAccountJSON: b})
	require.NoError(t, err)
}

func TestNewSender_ValidatesFields(t *testing.T) {
	t.Parallel()
	_, err := fcm.NewSender(fcm.Options{
		ServiceAccount: &fcm.ServiceAccount{ProjectID: "p"}, // missing email + key
	})
	require.Error(t, err)
}
