package cdc_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/golusoris/golusoris/outbox"
	outboxcdc "github.com/golusoris/golusoris/outbox/cdc"
)

func TestDefaultConfig(t *testing.T) {
	t.Parallel()
	d := outboxcdc.DefaultConfig()
	require.Equal(t, "golusoris_outbox", d.Table)
	require.Equal(t, "public", d.Schema)
}

func TestWebhookSink_Send(t *testing.T) {
	t.Parallel()
	var received []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		require.Equal(t, "mysecret", r.Header.Get("X-Webhook-Secret"))
		var err error
		received, err = io.ReadAll(r.Body)
		require.NoError(t, err)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sink := outboxcdc.NewWebhookSink(srv.URL,
		outboxcdc.WithWebhookSecret("mysecret"),
	)

	ev := outbox.Event{Kind: "order.created", Payload: json.RawMessage(`{"id":1}`), CreatedAt: time.Now()}
	require.NoError(t, sink.Send(context.Background(), ev))
	require.NotEmpty(t, received)
}

func TestWebhookSink_ErrorStatus(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	sink := outboxcdc.NewWebhookSink(srv.URL)
	ev := outbox.Event{Kind: "x", Payload: json.RawMessage(`{}`)}
	require.Error(t, sink.Send(context.Background(), ev))
}
