package resend_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/notify"
	"github.com/golusoris/golusoris/notify/resend"
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
		require.Equal(t, "noreply@example.com", got["from"])
		require.Equal(t, "Welcome", got["subject"])
		require.Equal(t, []any{"alice@example.com"}, got["to"])
		require.Equal(t, "<p>Hi</p>", got["html"])
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"abc"}`))
	}))
	t.Cleanup(srv.Close)

	s, err := resend.NewSender(resend.Options{
		APIKey:   "test-key",
		From:     "noreply@example.com",
		Endpoint: srv.URL,
	})
	require.NoError(t, err)
	require.Equal(t, "resend", s.Name())
	require.NoError(t, s.Send(context.Background(), notify.Message{
		To:      []string{"alice@example.com"},
		Subject: "Welcome",
		HTML:    "<p>Hi</p>",
	}))
}

func TestSender_RejectsEmptyBody(t *testing.T) {
	t.Parallel()
	s, err := resend.NewSender(resend.Options{
		APIKey: "k",
		From:   "noreply@example.com",
	})
	require.NoError(t, err)
	require.Error(t, s.Send(context.Background(), notify.Message{
		To:      []string{"x@example.com"},
		Subject: "no body",
	}))
}

func TestSender_RejectsNoRecipients(t *testing.T) {
	t.Parallel()
	s, _ := resend.NewSender(resend.Options{
		APIKey: "k",
		From:   "noreply@example.com",
	})
	require.Error(t, s.Send(context.Background(), notify.Message{HTML: "<p>x</p>"}))
}

func TestSender_NewRequiresAPIKey(t *testing.T) {
	t.Parallel()
	_, err := resend.NewSender(resend.Options{From: "x@example.com"})
	require.Error(t, err)
}

func TestSender_NewRequiresFrom(t *testing.T) {
	t.Parallel()
	_, err := resend.NewSender(resend.Options{APIKey: "k"})
	require.Error(t, err)
}

func TestSender_NonOK(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"invalid api key"}`))
	}))
	t.Cleanup(srv.Close)
	s, _ := resend.NewSender(resend.Options{
		APIKey:   "bad",
		From:     "x@example.com",
		Endpoint: srv.URL,
	})
	require.Error(t, s.Send(context.Background(), notify.Message{
		To:      []string{"alice@example.com"},
		Subject: "x",
		Text:    "hi",
	}))
}

func TestSender_TagsFromMetadata(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var got map[string]any
		_ = json.Unmarshal(body, &got)
		tags, _ := got["tags"].([]any)
		require.Len(t, tags, 1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	s, _ := resend.NewSender(resend.Options{
		APIKey:   "k",
		From:     "x@example.com",
		Endpoint: srv.URL,
	})
	require.NoError(t, s.Send(context.Background(), notify.Message{
		To:       []string{"alice@example.com"},
		Subject:  "x",
		Text:     "hi",
		Metadata: map[string]string{"campaign": "welcome"},
	}))
}
