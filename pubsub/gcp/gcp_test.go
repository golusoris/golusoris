// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gcp_test

import (
	"context"
	"errors"
	"testing"
	"time"

	pubsub "cloud.google.com/go/pubsub/v2"
	"cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"cloud.google.com/go/pubsub/v2/pstest"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/golusoris/golusoris/pubsub/gcp"
)

// maxPollIterations bounds every polling loop below (HISS-02: scalar upper
// bound on loops) instead of looping until a deadline with no cap.
const maxPollIterations = 1000

// TestPackage_compiles verifies the package exports are reachable without
// any network, mirroring pubsub/nats's equivalent smoke test.
func TestPackage_compiles(t *testing.T) {
	t.Parallel()
	_ = gcp.Module
}

// newTestClient starts an in-process pstest fake server, dials it, and
// returns a *pubsub.Client wired to it plus the fake server for assertions.
// Both the server and the underlying gRPC connection are closed via
// t.Cleanup; the returned *pubsub.Client is NOT closed here because the
// gcp.Client wrapping it owns that via [gcp.Client.Close].
func newTestClient(t *testing.T, projectID string) (*pubsub.Client, *pstest.Server) {
	t.Helper()

	srv := pstest.NewServer()
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Logf("pstest server close: %v", err)
		}
	})

	conn, err := grpc.NewClient(srv.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	pc, err := pubsub.NewClient(context.Background(), projectID, option.WithGRPCConn(conn))
	if err != nil {
		t.Fatalf("pubsub.NewClient: %v", err)
	}
	return pc, srv
}

// createTopicAndSub creates topicID and a subID subscription bound to it via
// the admin clients, using project-scoped resource names as the v2 API
// requires.
func createTopicAndSub(t *testing.T, pc *pubsub.Client, project, topicID, subID string) {
	t.Helper()
	ctx := context.Background()

	topicName := "projects/" + project + "/topics/" + topicID
	topic, err := pc.TopicAdminClient.CreateTopic(ctx, &pubsubpb.Topic{Name: topicName})
	if err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	subName := "projects/" + project + "/subscriptions/" + subID
	if _, err := pc.SubscriptionAdminClient.CreateSubscription(ctx, &pubsubpb.Subscription{
		Name:  subName,
		Topic: topic.GetName(),
	}); err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
}

// waitUntil polls cond every 10ms until it returns true or maxPollIterations
// is reached (HISS-02: bounded loop), failing the test on timeout.
func waitUntil(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	for range maxPollIterations {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(msg)
}

// TestPublishSubscribeRoundTrip is the positive case: a published message is
// delivered to the subscriber with its data and attributes intact.
func TestPublishSubscribeRoundTrip(t *testing.T) {
	t.Parallel()

	const project = "proj-roundtrip"
	pc, _ := newTestClient(t, project)
	c := gcp.ClientFromPubsub(pc)
	t.Cleanup(func() { _ = c.Close() })
	createTopicAndSub(t, pc, project, "orders", "orders-sub")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	type received struct {
		data  []byte
		attrs map[string]string
	}
	got := make(chan received, 1)
	go func() {
		_ = c.Subscribe(ctx, "orders-sub", func(_ context.Context, m *gcp.Message) {
			got <- received{data: m.Data, attrs: m.Attributes}
			m.Ack()
		})
	}()

	id, err := c.Publish(context.Background(), "orders", []byte("hello-gcp"), map[string]string{"kind": "order"})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty message ID")
	}

	select {
	case r := <-got:
		if string(r.data) != "hello-gcp" {
			t.Errorf("data = %q, want %q", r.data, "hello-gcp")
		}
		if r.attrs["kind"] != "order" {
			t.Errorf(`attrs["kind"] = %q, want "order"`, r.attrs["kind"])
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for message")
	}
}

// TestAckNackRedelivery covers the negative/retry path: a Nacked message is
// redelivered, and only the Acked delivery counts as processed on the server.
func TestAckNackRedelivery(t *testing.T) {
	t.Parallel()

	const project = "proj-acknack"
	pc, srv := newTestClient(t, project)
	c := gcp.ClientFromPubsub(pc)
	t.Cleanup(func() { _ = c.Close() })
	createTopicAndSub(t, pc, project, "retry-topic", "retry-sub")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	var deliveries int
	acked := make(chan string, 1)
	go func() {
		_ = c.Subscribe(ctx, "retry-sub", func(_ context.Context, m *gcp.Message) {
			deliveries++
			if deliveries == 1 {
				m.Nack()
				return
			}
			m.Ack()
			acked <- m.ID
		})
	}()

	msgID, err := c.Publish(context.Background(), "retry-topic", []byte("retry-me"), nil)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	var ackedID string
	select {
	case ackedID = <-acked:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the post-nack ack")
	}
	if ackedID != msgID {
		t.Errorf("acked message ID = %q, want %q", ackedID, msgID)
	}

	waitUntil(t, func() bool {
		m := srv.Message(msgID)
		return m != nil && m.Deliveries >= 2 && m.Acks >= 1
	}, "server never recorded a redelivery followed by an ack")
}

// TestClosedClientBoundary is the boundary case: every I/O method on a
// closed Client returns [gcp.ErrClosed] instead of touching the network,
// and Close itself is idempotent.
func TestClosedClientBoundary(t *testing.T) {
	t.Parallel()

	pc, _ := newTestClient(t, "proj-closed")
	c := gcp.ClientFromPubsub(pc)

	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close (idempotency): %v", err)
	}

	ctx := context.Background()
	if _, err := c.Publish(ctx, "any-topic", []byte("x"), nil); !errors.Is(err, gcp.ErrClosed) {
		t.Errorf("Publish after Close: got %v, want ErrClosed", err)
	}
	if err := c.Subscribe(ctx, "any-sub", func(context.Context, *gcp.Message) {}); !errors.Is(err, gcp.ErrClosed) {
		t.Errorf("Subscribe after Close: got %v, want ErrClosed", err)
	}
	if err := c.Ping(ctx); !errors.Is(err, gcp.ErrClosed) {
		t.Errorf("Ping after Close: got %v, want ErrClosed", err)
	}
}

// TestPing_ListsEmptyProject is a boundary case for Ping itself: an empty
// project (no topics) must report healthy, not an error.
func TestPing_ListsEmptyProject(t *testing.T) {
	t.Parallel()

	pc, _ := newTestClient(t, "proj-empty")
	c := gcp.ClientFromPubsub(pc)
	t.Cleanup(func() { _ = c.Close() })

	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping on empty project: %v", err)
	}
}
