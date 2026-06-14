package gotify_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/notify"
	"github.com/golusoris/golusoris/notify/gotify"
)

func TestSender_Send(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		opts         gotify.Options
		msg          notify.Message
		wantTitle    string
		wantMessage  string
		wantPriority float64
		wantClick    string
		wantIcon     string
	}{
		{
			name:         "body and subject",
			opts:         gotify.Options{Priority: 5},
			msg:          notify.Message{Subject: "Deploy", Body: "shipped"},
			wantTitle:    "Deploy",
			wantMessage:  "shipped",
			wantPriority: 5,
		},
		{
			name:         "text fallback",
			opts:         gotify.Options{Priority: 2},
			msg:          notify.Message{Text: "from text"},
			wantTitle:    "",
			wantMessage:  "from text",
			wantPriority: 2,
		},
		{
			name:         "metadata priority overrides",
			opts:         gotify.Options{Priority: 1},
			msg:          notify.Message{Body: "urgent", Metadata: map[string]string{"priority": "8"}},
			wantMessage:  "urgent",
			wantPriority: 8,
		},
		{
			name:         "click and icon extras",
			opts:         gotify.Options{Priority: 3},
			msg:          notify.Message{Body: "open it", Metadata: map[string]string{"click": "https://app.example/req/7", "icon": "https://img.example/poster.png"}},
			wantMessage:  "open it",
			wantPriority: 3,
			wantClick:    "https://app.example/req/7",
			wantIcon:     "https://img.example/poster.png",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "application/json", r.Header.Get("Content-Type"))
				require.Equal(t, "/message", r.URL.Path)
				require.Equal(t, "tok123", r.URL.Query().Get("token"))
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				var got map[string]any
				require.NoError(t, json.Unmarshal(body, &got))
				require.Equal(t, tt.wantMessage, got["message"])
				require.Equal(t, tt.wantPriority, got["priority"])
				if tt.wantTitle != "" {
					require.Equal(t, tt.wantTitle, got["title"])
				} else {
					_, hasTitle := got["title"]
					require.False(t, hasTitle)
				}
				if tt.wantClick != "" || tt.wantIcon != "" {
					extras, ok := got["extras"].(map[string]any)
					require.True(t, ok, "extras must be present")
					if tt.wantClick != "" {
						click, ok := extras["client::notification.click"].(map[string]any)
						require.True(t, ok)
						require.Equal(t, tt.wantClick, click["url"])
					}
					if tt.wantIcon != "" {
						img, ok := extras["client::notification.bigImageUrl"].(map[string]any)
						require.True(t, ok)
						require.Equal(t, tt.wantIcon, img["imageUrl"])
					}
				} else {
					_, hasExtras := got["extras"]
					require.False(t, hasExtras)
				}
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(srv.Close)

			tt.opts.ServerURL = srv.URL
			tt.opts.AppToken = "tok123"
			s, err := gotify.NewSender(tt.opts)
			require.NoError(t, err)
			require.Equal(t, "gotify", s.Name())
			require.NoError(t, s.Send(context.Background(), tt.msg))
		})
	}
}

func TestNewSender_Validation(t *testing.T) {
	t.Parallel()
	_, err := gotify.NewSender(gotify.Options{AppToken: "x"})
	require.Error(t, err)
	_, err = gotify.NewSender(gotify.Options{ServerURL: "https://gotify.example.com"})
	require.Error(t, err)
}

func TestSender_Empty(t *testing.T) {
	t.Parallel()
	s, err := gotify.NewSender(gotify.Options{ServerURL: "http://example.invalid", AppToken: "x"})
	require.NoError(t, err)
	require.Error(t, s.Send(context.Background(), notify.Message{}))
}

func TestSender_NonOK(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	t.Cleanup(srv.Close)
	s, err := gotify.NewSender(gotify.Options{ServerURL: srv.URL, AppToken: "bad"})
	require.NoError(t, err)
	require.Error(t, s.Send(context.Background(), notify.Message{Body: "x"}))
}
