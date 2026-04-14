package teams_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/notify"
	"github.com/golusoris/golusoris/notify/teams"
)

func TestSender_Send(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var got map[string]any
		require.NoError(t, json.Unmarshal(body, &got))
		require.Equal(t, "MessageCard", got["@type"])
		require.Equal(t, "Deploy completed", got["title"])
		require.Equal(t, "All services healthy.", got["text"])
		require.Equal(t, "0076D7", got["themeColor"])

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`1`))
	}))
	t.Cleanup(srv.Close)

	s, err := teams.NewSender(teams.Options{WebhookURL: srv.URL, ThemeColor: "0076D7"})
	require.NoError(t, err)
	require.Equal(t, "teams", s.Name())
	require.NoError(t, s.Send(context.Background(), notify.Message{
		Subject: "Deploy completed",
		Body:    "All services healthy.",
	}))
}

func TestSender_RejectsEmpty(t *testing.T) {
	t.Parallel()
	s, _ := teams.NewSender(teams.Options{WebhookURL: "http://unused"})
	require.Error(t, s.Send(context.Background(), notify.Message{}))
}

func TestSender_RejectsMissingURL(t *testing.T) {
	t.Parallel()
	_, err := teams.NewSender(teams.Options{})
	require.Error(t, err)
}

func TestSender_MultilineBodyUsesFirstLine(t *testing.T) {
	t.Parallel()
	var gotSummary string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var card map[string]any
		_ = json.Unmarshal(body, &card)
		if s, ok := card["summary"].(string); ok {
			gotSummary = s
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`1`))
	}))
	t.Cleanup(srv.Close)

	s, err := teams.NewSender(teams.Options{WebhookURL: srv.URL})
	require.NoError(t, err)
	require.NoError(t, s.Send(context.Background(), notify.Message{
		Body: "first line\nsecond line\nthird line",
	}))
	require.Equal(t, "first line", gotSummary)
}
