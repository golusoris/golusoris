package slack_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/notify"
	"github.com/golusoris/golusoris/notify/slack"
)

func TestSender_Send(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		body, _ := io.ReadAll(r.Body)
		var got map[string]string
		require.NoError(t, json.Unmarshal(body, &got))
		require.Equal(t, "alert", got["text"])
		require.Equal(t, "#ops", got["channel"])
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	s, err := slack.NewSender(slack.Options{WebhookURL: srv.URL, Channel: "#ops"})
	require.NoError(t, err)
	require.Equal(t, "slack", s.Name())
	require.NoError(t, s.Send(context.Background(), notify.Message{Body: "alert"}))
}

func TestSender_FallbackBody(t *testing.T) {
	t.Parallel()
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p struct{ Text string }
		_ = json.Unmarshal(body, &p)
		got = p.Text
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	s, err := slack.NewSender(slack.Options{WebhookURL: srv.URL})
	require.NoError(t, err)
	require.NoError(t, s.Send(context.Background(), notify.Message{Subject: "Outage", Text: "Service degraded"}))
	require.Contains(t, got, "*Outage*")
	require.Contains(t, got, "Service degraded")
}

func TestSender_Failures(t *testing.T) {
	t.Parallel()
	_, err := slack.NewSender(slack.Options{})
	require.Error(t, err)
	s, _ := slack.NewSender(slack.Options{WebhookURL: "http://example.invalid"})
	require.Error(t, s.Send(context.Background(), notify.Message{}))
}
