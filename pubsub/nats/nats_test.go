// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package nats_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/pubsub/cloudevents"
	"github.com/golusoris/golusoris/pubsub/nats"
	natstestutil "github.com/golusoris/golusoris/testutil/nats"
)

// TestPackage_compiles verifies the package exports are reachable without Docker.
func TestPackage_compiles(t *testing.T) {
	t.Parallel()
	_ = nats.Module
}

// bootClient starts a *nats.Client via the fx lifecycle wired to url.
// The app is stopped via t.Cleanup.
func bootClient(t *testing.T, url string) *nats.Client {
	t.Helper()

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("nats:\n  url: "+url+"\n  name: test\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.New(config.Options{Files: []string{cfgPath}})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}

	var client *nats.Client
	app := fxtest.New(
		t,
		fx.Provide(func() *config.Config { return cfg }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		nats.Module,
		fx.Populate(&client),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)
	return client
}

// TestIntegration_ConnectAndPing confirms the Module wires up and connects.
func TestIntegration_ConnectAndPing(t *testing.T) {
	t.Parallel()

	url := natstestutil.Start(t)
	c := bootClient(t, url)
	if !c.Conn().IsConnected() {
		t.Fatal("expected NATS connection to be connected")
	}
}

// TestIntegration_PublishSubscribe exercises core pub/sub delivery end-to-end.
func TestIntegration_PublishSubscribe(t *testing.T) {
	t.Parallel()

	url := natstestutil.Start(t)
	c := bootClient(t, url)

	ch := make(chan []byte, 1)
	sub, err := c.Subscribe("test.subject", func(msg *natsgo.Msg) {
		ch <- msg.Data
	})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	want := []byte("hello-nats")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.PublishSync(ctx, "test.subject", want); err != nil {
		t.Fatalf("PublishSync: %v", err)
	}

	select {
	case got := <-ch:
		if string(got) != string(want) {
			t.Errorf("got %q, want %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for message")
	}
}

// TestIntegration_JetStreamAvailable confirms the JetStream context is usable.
func TestIntegration_JetStreamAvailable(t *testing.T) {
	t.Parallel()

	url := natstestutil.Start(t)
	c := bootClient(t, url)
	if c.JetStream() == nil {
		t.Fatal("expected non-nil JetStream context")
	}
}

// TestIntegration_PublishCloudEventDedupes publishes one event twice per
// content mode; JetStream stores it once and the stored message decodes back.
func TestIntegration_PublishCloudEventDedupes(t *testing.T) {
	t.Parallel()

	url := natstestutil.Start(t)
	c := bootClient(t, url)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stream, err := c.JetStream().CreateStream(ctx, jetstream.StreamConfig{
		Name: "EVENTS", Subjects: []string{"events.>"}, Duplicates: time.Minute,
	})
	require.NoError(t, err)

	for i, mode := range []cloudevents.Mode{cloudevents.ModeBinary, cloudevents.ModeStructured} {
		ev := sampleEvent("evt-" + mode.String())
		first, pubErr := c.PublishCloudEvent(ctx, "events.jobs."+mode.String(), ev, mode)
		require.NoError(t, pubErr)
		require.False(t, first.Duplicate)
		again, pubErr := c.PublishCloudEvent(ctx, "events.jobs."+mode.String(), ev, mode)
		require.NoError(t, pubErr)
		require.True(t, again.Duplicate, "same event id must be dropped by Nats-Msg-Id")
		require.Equal(t, first.Sequence, again.Sequence)

		info, infoErr := stream.Info(ctx)
		require.NoError(t, infoErr)
		require.Equal(t, uint64(i+1), info.State.Msgs)

		stored, getErr := stream.GetMsg(ctx, first.Sequence)
		require.NoError(t, getErr)
		require.Equal(t, ev.ID, stored.Header.Get(jetstream.MsgIDHeader))
		back, decErr := nats.DecodeCloudEvent(stored.Header, stored.Data)
		require.NoError(t, decErr)
		requireSameEvent(t, ev, back)
	}
}

// TestIntegration_PublishCloudEventWithoutStreamFails proves the publish waits
// for a PubAck instead of reporting a fire-and-forget success.
func TestIntegration_PublishCloudEventWithoutStreamFails(t *testing.T) {
	t.Parallel()

	url := natstestutil.Start(t)
	c := bootClient(t, url)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := c.PublishCloudEvent(ctx, "unbound.subject", sampleEvent("evt-1"), cloudevents.ModeBinary)
	require.ErrorIs(t, err, jetstream.ErrNoStreamResponse)
}
