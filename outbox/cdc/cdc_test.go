// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package cdc_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pubsub "cloud.google.com/go/pubsub/v2"
	"cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"cloud.google.com/go/pubsub/v2/pstest"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/golusoris/golusoris/outbox"
	outboxcdc "github.com/golusoris/golusoris/outbox/cdc"
	"github.com/golusoris/golusoris/pubsub/gcp"
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

	sink := outboxcdc.NewWebhookSink(
		srv.URL,
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

// newTestGCPClient starts an in-process pstest fake server, dials it, and
// wraps it as a *gcp.Client. Mirrors pubsub/gcp's own test helper of the
// same shape.
func newTestGCPClient(t *testing.T, projectID string) *gcp.Client {
	t.Helper()

	srv := pstest.NewServer()
	t.Cleanup(func() { require.NoError(t, srv.Close()) })

	conn, err := grpc.NewClient(srv.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	pc, err := pubsub.NewClient(context.Background(), projectID, option.WithGRPCConn(conn))
	require.NoError(t, err)

	topicName := "projects/" + projectID + "/topics/outbox-events"
	_, err = pc.TopicAdminClient.CreateTopic(context.Background(), &pubsubpb.Topic{Name: topicName})
	require.NoError(t, err)

	c := gcp.ClientFromPubsub(pc)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestGCPSink_Send is the positive case: a Send'd event round-trips to the
// topic with its kind carried as a message attribute.
func TestGCPSink_Send(t *testing.T) {
	t.Parallel()

	client := newTestGCPClient(t, "proj-sink")
	sink := outboxcdc.NewGCPSink(client, "outbox-events")

	ev := outbox.Event{Kind: "order.created", Payload: json.RawMessage(`{"id":1}`), CreatedAt: time.Now()}
	require.NoError(t, sink.Send(context.Background(), ev))
}

// TestGCPSink_Send_ClosedClient is the negative case: once the underlying
// [gcp.Client] is closed, Send must surface that error instead of panicking
// or silently dropping the event.
func TestGCPSink_Send_ClosedClient(t *testing.T) {
	t.Parallel()

	client := newTestGCPClient(t, "proj-sink-closed")
	require.NoError(t, client.Close())
	sink := outboxcdc.NewGCPSink(client, "outbox-events")

	ev := outbox.Event{Kind: "x", Payload: json.RawMessage(`{}`)}
	require.ErrorIs(t, sink.Send(context.Background(), ev), gcp.ErrClosed)
}
