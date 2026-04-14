package sendgrid_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/notify"
	"github.com/golusoris/golusoris/notify/sendgrid"
)

func TestSender_Send(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var got map[string]any
		require.NoError(t, json.Unmarshal(body, &got))
		require.Equal(t, "Welcome", got["subject"])
		from, _ := got["from"].(map[string]any)
		require.Equal(t, "noreply@example.com", from["email"])

		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)

	s, err := sendgrid.NewSender(sendgrid.Options{
		APIKey:   "test-key",
		From:     "noreply@example.com",
		Endpoint: srv.URL,
	})
	require.NoError(t, err)
	require.Equal(t, "sendgrid", s.Name())
	require.NoError(t, s.Send(context.Background(), notify.Message{
		To:      []string{"alice@example.com"},
		Subject: "Welcome",
		HTML:    "<p>Hi</p>",
		Text:    "Hi",
	}))
}

func TestSender_RejectsEmptyBody(t *testing.T) {
	t.Parallel()
	s, err := sendgrid.NewSender(sendgrid.Options{APIKey: "k", From: "x@y.com"})
	require.NoError(t, err)
	require.Error(t, s.Send(context.Background(), notify.Message{To: []string{"a@b.com"}, Subject: "x"}))
}

func TestSender_RejectsNoRecipients(t *testing.T) {
	t.Parallel()
	s, _ := sendgrid.NewSender(sendgrid.Options{APIKey: "k", From: "x@y.com"})
	require.Error(t, s.Send(context.Background(), notify.Message{HTML: "<p>x</p>"}))
}

func TestSender_RejectsMissingRequired(t *testing.T) {
	t.Parallel()
	_, err := sendgrid.NewSender(sendgrid.Options{From: "x@y.com"})
	require.Error(t, err)
	_, err = sendgrid.NewSender(sendgrid.Options{APIKey: "k"})
	require.Error(t, err)
}
