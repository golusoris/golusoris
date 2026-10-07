// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package postmark_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/notify"
	"github.com/golusoris/golusoris/notify/postmark"
)

func TestNewBasicAuthVerifier(t *testing.T) {
	t.Parallel()
	verify, err := postmark.NewBasicAuthVerifier("postmark-user", strings.Repeat("p", 32))
	require.NoError(t, err)

	for _, tt := range []struct {
		name     string
		username string
		password string
		wantErr  bool
	}{
		{name: "matching credentials", username: "postmark-user", password: strings.Repeat("p", 32)},
		{name: "wrong username", username: "forged-user", password: strings.Repeat("p", 32), wantErr: true},
		{name: "wrong password", username: "postmark-user", password: strings.Repeat("x", 32), wantErr: true},
		{name: "missing credentials", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, "/webhook", nil)
			if tt.username != "" || tt.password != "" {
				req.SetBasicAuth(tt.username, tt.password)
			}

			err := verify(req, nil)

			if tt.wantErr {
				require.ErrorIs(t, err, postmark.ErrWebhookUnauthorized)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestNewBasicAuthVerifierRejectsEmptyCredentials(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		username string
		password string
	}{
		{name: "empty username", password: "password"},
		{name: "empty password", username: "username"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			verify, err := postmark.NewBasicAuthVerifier(tt.username, tt.password)
			require.Error(t, err)
			require.Nil(t, verify)
			require.False(t, errors.Is(err, postmark.ErrWebhookUnauthorized))
		})
	}
}

func TestSender_Send(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "test-token", r.Header.Get("X-Postmark-Server-Token"))
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var got map[string]any
		require.NoError(t, json.Unmarshal(body, &got))
		require.Equal(t, "noreply@example.com", got["From"])
		require.Equal(t, "alice@example.com,bob@example.com", got["To"])
		require.Equal(t, "Welcome", got["Subject"])
		require.Equal(t, "<p>Hi</p>", got["HtmlBody"])
		require.Equal(t, "outbound", got["MessageStream"])
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"MessageID":"abc"}`))
	}))
	t.Cleanup(srv.Close)

	s, err := postmark.NewSender(postmark.Options{
		ServerToken:   "test-token",
		From:          "noreply@example.com",
		MessageStream: "outbound",
		Endpoint:      srv.URL,
	})
	require.NoError(t, err)
	require.Equal(t, "postmark", s.Name())
	require.NoError(t, s.Send(context.Background(), notify.Message{
		To:      []string{"alice@example.com", "bob@example.com"},
		Subject: "Welcome",
		HTML:    "<p>Hi</p>",
	}))
}

func TestSender_RejectsEmptyBody(t *testing.T) {
	t.Parallel()
	s, _ := postmark.NewSender(postmark.Options{
		ServerToken: "k",
		From:        "noreply@example.com",
	})
	require.Error(t, s.Send(context.Background(), notify.Message{
		To:      []string{"x@example.com"},
		Subject: "no body",
	}))
}

func TestSender_RejectsNoRecipients(t *testing.T) {
	t.Parallel()
	s, _ := postmark.NewSender(postmark.Options{
		ServerToken: "k",
		From:        "noreply@example.com",
	})
	require.Error(t, s.Send(context.Background(), notify.Message{HTML: "<p>x</p>"}))
}

func TestSender_NewRequiresToken(t *testing.T) {
	t.Parallel()
	_, err := postmark.NewSender(postmark.Options{From: "x@example.com"})
	require.Error(t, err)
}

func TestSender_NewRequiresFrom(t *testing.T) {
	t.Parallel()
	_, err := postmark.NewSender(postmark.Options{ServerToken: "k"})
	require.Error(t, err)
}

func TestSender_NonOK(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"ErrorCode":300,"Message":"invalid email"}`))
	}))
	t.Cleanup(srv.Close)
	s, _ := postmark.NewSender(postmark.Options{
		ServerToken: "k",
		From:        "x@example.com",
		Endpoint:    srv.URL,
	})
	require.Error(t, s.Send(context.Background(), notify.Message{
		To:      []string{"alice@example.com"},
		Subject: "x",
		Text:    "hi",
	}))
}
