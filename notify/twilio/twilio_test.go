package twilio_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/notify"
	"github.com/golusoris/golusoris/notify/twilio"
)

func TestSender_SendSingle(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/Accounts/AC123/Messages.json", r.URL.Path)

		sid, tok, ok := r.BasicAuth()
		require.True(t, ok)
		require.Equal(t, "AC123", sid)
		require.Equal(t, "secret", tok)

		require.Equal(t, "application/x-www-form-urlencoded", r.Header.Get("Content-Type"))
		body, _ := io.ReadAll(r.Body)
		form, err := url.ParseQuery(string(body))
		require.NoError(t, err)
		require.Equal(t, "+15557654321", form.Get("To"))
		require.Equal(t, "+15551234567", form.Get("From"))
		require.Equal(t, "hello", form.Get("Body"))

		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"sid":"SM123"}`))
	}))
	t.Cleanup(srv.Close)

	s, err := twilio.NewSender(twilio.Options{
		AccountSID: "AC123",
		AuthToken:  "secret",
		From:       "+15551234567",
		Endpoint:   srv.URL,
	})
	require.NoError(t, err)
	require.Equal(t, "twilio", s.Name())
	require.NoError(t, s.Send(context.Background(), notify.Message{
		To:   []string{"+15557654321"},
		Body: "hello",
	}))
}

func TestSender_SendMultipleRecipients(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)

	s, _ := twilio.NewSender(twilio.Options{
		AccountSID: "AC123", AuthToken: "s",
		MessagingServiceSID: "MG1",
		Endpoint:            srv.URL,
	})
	require.NoError(t, s.Send(context.Background(), notify.Message{
		To:   []string{"+1", "+2", "+3"},
		Body: "x",
	}))
	require.Equal(t, int32(3), calls.Load())
}

func TestSender_RejectsMissingRequired(t *testing.T) {
	t.Parallel()
	_, err := twilio.NewSender(twilio.Options{AuthToken: "x", From: "+1"})
	require.Error(t, err)
	_, err = twilio.NewSender(twilio.Options{AccountSID: "x", From: "+1"})
	require.Error(t, err)
	_, err = twilio.NewSender(twilio.Options{AccountSID: "x", AuthToken: "y"})
	require.Error(t, err)
	_, err = twilio.NewSender(twilio.Options{
		AccountSID: "x", AuthToken: "y", From: "+1", MessagingServiceSID: "MG1",
	})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "mutually exclusive"))
}
