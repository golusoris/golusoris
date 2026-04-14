package telegram_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/notify"
	"github.com/golusoris/golusoris/notify/telegram"
)

func TestSender_Send(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.True(t, strings.HasPrefix(r.URL.Path, "/bot123:ABC/"))
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var got map[string]any
		require.NoError(t, json.Unmarshal(body, &got))
		require.Equal(t, "-1001234567890", got["chat_id"])
		require.Equal(t, "Deploy completed\n\nAll services are healthy.", got["text"])
		require.Equal(t, "HTML", got["parse_mode"])

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)

	s, err := telegram.NewSender(telegram.Options{
		BotToken:  "123:ABC",
		ChatID:    "-1001234567890",
		ParseMode: telegram.ParseModeHTML,
		Endpoint:  srv.URL,
	})
	require.NoError(t, err)
	require.Equal(t, "telegram", s.Name())
	require.NoError(t, s.Send(context.Background(), notify.Message{
		Subject: "Deploy completed",
		Text:    "All services are healthy.",
	}))
}

func TestSender_MsgToOverridesChatID(t *testing.T) {
	t.Parallel()
	var gotChat string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var got map[string]any
		_ = json.Unmarshal(body, &got)
		gotChat, _ = got["chat_id"].(string)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	s, _ := telegram.NewSender(telegram.Options{
		BotToken: "t", ChatID: "@default_chan", Endpoint: srv.URL,
	})
	require.NoError(t, s.Send(context.Background(), notify.Message{
		To:   []string{"@override_chan"},
		Body: "hi",
	}))
	require.Equal(t, "@override_chan", gotChat)
}

func TestSender_RejectsMissingChat(t *testing.T) {
	t.Parallel()
	s, _ := telegram.NewSender(telegram.Options{BotToken: "t"})
	require.Error(t, s.Send(context.Background(), notify.Message{Body: "hi"}))
}

func TestSender_RejectsEmptyBody(t *testing.T) {
	t.Parallel()
	s, _ := telegram.NewSender(telegram.Options{BotToken: "t", ChatID: "1"})
	require.Error(t, s.Send(context.Background(), notify.Message{}))
}

func TestSender_RejectsMissingToken(t *testing.T) {
	t.Parallel()
	_, err := telegram.NewSender(telegram.Options{ChatID: "1"})
	require.Error(t, err)
}
