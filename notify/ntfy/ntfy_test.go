package ntfy_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/notify"
	"github.com/golusoris/golusoris/notify/ntfy"
)

func TestSender_Send(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		opts         ntfy.Options
		msg          notify.Message
		wantBody     string
		wantTitle    string
		wantPriority string
		wantTags     string
		wantAuth     string
		wantClick    string
		wantIcon     string
	}{
		{
			name:         "body with defaults",
			opts:         ntfy.Options{Priority: 4, Tags: []string{"warning", "skull"}},
			msg:          notify.Message{Subject: "Alert", Body: "disk full"},
			wantBody:     "disk full",
			wantTitle:    "Alert",
			wantPriority: "4",
			wantTags:     "warning,skull",
		},
		{
			name:      "text fallback no priority",
			opts:      ntfy.Options{},
			msg:       notify.Message{Text: "hello"},
			wantBody:  "hello",
			wantTitle: "",
		},
		{
			name:         "metadata overrides",
			opts:         ntfy.Options{Priority: 1, Tags: []string{"default"}},
			msg:          notify.Message{Body: "x", Metadata: map[string]string{"priority": "5", "tags": "fire"}},
			wantBody:     "x",
			wantPriority: "5",
			wantTags:     "fire",
		},
		{
			name:     "bearer token auth",
			opts:     ntfy.Options{Token: "tk_secret"},
			msg:      notify.Message{Body: "authed"},
			wantBody: "authed",
			wantAuth: "Bearer tk_secret",
		},
		{
			name:     "basic auth",
			opts:     ntfy.Options{Username: "user", Password: "pass"},
			msg:      notify.Message{Body: "basic"},
			wantBody: "basic",
			wantAuth: "Basic dXNlcjpwYXNz",
		},
		{
			name:      "click and icon headers",
			opts:      ntfy.Options{},
			msg:       notify.Message{Body: "open", Metadata: map[string]string{"click": "https://app.example/req/7", "icon": "https://img.example/poster.png"}},
			wantBody:  "open",
			wantClick: "https://app.example/req/7",
			wantIcon:  "https://img.example/poster.png",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/alerts", r.URL.Path)
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				require.Equal(t, tt.wantBody, string(body))
				require.Equal(t, tt.wantTitle, r.Header.Get("Title"))
				require.Equal(t, tt.wantPriority, r.Header.Get("Priority"))
				require.Equal(t, tt.wantTags, r.Header.Get("Tags"))
				require.Equal(t, tt.wantAuth, r.Header.Get("Authorization"))
				require.Equal(t, tt.wantClick, r.Header.Get("Click"))
				require.Equal(t, tt.wantIcon, r.Header.Get("Icon"))
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(srv.Close)

			tt.opts.ServerURL = srv.URL
			tt.opts.Topic = "alerts"
			s, err := ntfy.NewSender(tt.opts)
			require.NoError(t, err)
			require.Equal(t, "ntfy", s.Name())
			require.NoError(t, s.Send(context.Background(), tt.msg))
		})
	}
}

func TestNewSender_Validation(t *testing.T) {
	t.Parallel()
	_, err := ntfy.NewSender(ntfy.Options{Topic: "x"})
	require.Error(t, err)
	_, err = ntfy.NewSender(ntfy.Options{ServerURL: "https://ntfy.sh"})
	require.Error(t, err)
}

func TestSender_Empty(t *testing.T) {
	t.Parallel()
	s, err := ntfy.NewSender(ntfy.Options{ServerURL: "http://example.invalid", Topic: "t"})
	require.NoError(t, err)
	require.Error(t, s.Send(context.Background(), notify.Message{}))
}

func TestSender_NonOK(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`forbidden`))
	}))
	t.Cleanup(srv.Close)
	s, err := ntfy.NewSender(ntfy.Options{ServerURL: srv.URL, Topic: "t"})
	require.NoError(t, err)
	require.Error(t, s.Send(context.Background(), notify.Message{Body: "x"}))
}
