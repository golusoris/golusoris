package mailgun_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/notify"
	"github.com/golusoris/golusoris/notify/mailgun"
)

func TestSender_Send(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/mg.example.com/messages", r.URL.Path)

		user, pass, ok := r.BasicAuth()
		require.True(t, ok)
		require.Equal(t, "api", user)
		require.Equal(t, "test-key", pass)

		require.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		form, err := url.ParseQuery(string(body))
		require.NoError(t, err)
		require.Equal(t, "noreply@example.com", form.Get("from"))
		require.Equal(t, "alice@example.com", form.Get("to"))
		require.Equal(t, "Welcome", form.Get("subject"))
		require.Equal(t, "<p>Hi</p>", form.Get("html"))
		require.Equal(t, "mkt", form.Get("v:campaign"))

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"<abc@mg.example.com>","message":"Queued"}`))
	}))
	t.Cleanup(srv.Close)

	s, err := mailgun.NewSender(mailgun.Options{
		Domain:   "mg.example.com",
		APIKey:   "test-key",
		From:     "noreply@example.com",
		Endpoint: srv.URL,
	})
	require.NoError(t, err)
	require.Equal(t, "mailgun", s.Name())
	require.NoError(t, s.Send(context.Background(), notify.Message{
		To:       []string{"alice@example.com"},
		Subject:  "Welcome",
		HTML:     "<p>Hi</p>",
		Metadata: map[string]string{"campaign": "mkt"},
	}))
}

func TestSender_Error(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"bad key"}`))
	}))
	t.Cleanup(srv.Close)

	s, err := mailgun.NewSender(mailgun.Options{
		Domain: "mg.example.com", APIKey: "k", From: "x@y.com", Endpoint: srv.URL,
	})
	require.NoError(t, err)
	err = s.Send(context.Background(), notify.Message{To: []string{"a@b.com"}, HTML: "<p>x</p>"})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "401"))
}

func TestSender_RejectsMissingRequired(t *testing.T) {
	t.Parallel()
	_, err := mailgun.NewSender(mailgun.Options{APIKey: "k", From: "x@y.com"})
	require.Error(t, err)
	_, err = mailgun.NewSender(mailgun.Options{Domain: "d", From: "x@y.com"})
	require.Error(t, err)
	_, err = mailgun.NewSender(mailgun.Options{Domain: "d", APIKey: "k"})
	require.Error(t, err)
}

func TestSender_RejectsEmptyBody(t *testing.T) {
	t.Parallel()
	s, _ := mailgun.NewSender(mailgun.Options{Domain: "d", APIKey: "k", From: "x@y.com"})
	require.Error(t, s.Send(context.Background(), notify.Message{To: []string{"a@b.com"}, Subject: "x"}))
}

func TestSender_RejectsNoRecipients(t *testing.T) {
	t.Parallel()
	s, _ := mailgun.NewSender(mailgun.Options{Domain: "d", APIKey: "k", From: "x@y.com"})
	require.Error(t, s.Send(context.Background(), notify.Message{HTML: "<p>x</p>"}))
}
