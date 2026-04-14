package discord_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/notify"
	"github.com/golusoris/golusoris/notify/discord"
)

func TestSender_Send(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var got map[string]string
		require.NoError(t, json.Unmarshal(body, &got))
		require.Equal(t, "hello", got["content"])
		require.Equal(t, "bot", got["username"])
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	s, err := discord.NewSender(discord.Options{WebhookURL: srv.URL, Username: "bot"})
	require.NoError(t, err)
	require.Equal(t, "discord", s.Name())
	require.NoError(t, s.Send(context.Background(), notify.Message{Body: "hello"}))
}

func TestSender_FallbackBody(t *testing.T) {
	t.Parallel()
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p struct{ Content string }
		_ = json.Unmarshal(body, &p)
		got = p.Content
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	s, err := discord.NewSender(discord.Options{WebhookURL: srv.URL})
	require.NoError(t, err)
	require.NoError(t, s.Send(context.Background(), notify.Message{Subject: "Build green", Text: "All checks passed."}))
	require.Contains(t, got, "**Build green**")
	require.Contains(t, got, "All checks passed.")
}

func TestSender_Empty(t *testing.T) {
	t.Parallel()
	s, err := discord.NewSender(discord.Options{WebhookURL: "http://example.invalid"})
	require.NoError(t, err)
	require.Error(t, s.Send(context.Background(), notify.Message{}))
}

func TestSender_NewRequiresURL(t *testing.T) {
	t.Parallel()
	_, err := discord.NewSender(discord.Options{})
	require.Error(t, err)
}

func TestSender_NonOK(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"bad"}`))
	}))
	t.Cleanup(srv.Close)
	s, _ := discord.NewSender(discord.Options{WebhookURL: srv.URL})
	require.Error(t, s.Send(context.Background(), notify.Message{Body: "x"}))
}
