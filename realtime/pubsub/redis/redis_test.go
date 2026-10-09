// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package redis

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/rueidis"

	"github.com/golusoris/golusoris/realtime/pubsub"
	redistest "github.com/golusoris/golusoris/testutil/redis"
)

type typedNilClient struct{ rueidis.Client }

func TestEncode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"nil", nil, ""},
		{"bytes", []byte("raw"), "raw"},
		{"string", "hello", "hello"},
		{"struct", struct {
			A int `json:"a"`
		}{A: 1}, `{"a":1}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := encode(tt.in)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if got != tt.want {
				t.Errorf("encode(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNewNormalizesTypedNilClient(t *testing.T) {
	t.Parallel()
	var client *typedNilClient
	bus := New(client, nil)
	if bus.client != nil {
		t.Fatal("typed-nil client was retained")
	}
}

// TestPubSubRoundTrip publishes to a topic and asserts a subscriber on the same
// bus receives it, across a real Redis (testcontainers; requires Docker).
func TestPubSubRoundTrip(t *testing.T) {
	t.Parallel()
	client := redistest.Start(t)
	bus := New(client, slog.New(slog.DiscardHandler))
	got := make(chan []byte, 1)
	defer bus.Subscribe("test.topic", collect(got))()
	awaitMessage(t, bus, "test.topic", "hello", got)
}

// TestSubscribeSurvivesConnectionDrop kills the subscriber's connection and
// asserts a message published afterwards still arrives and onGap fired once,
// with and without rueidis's own retry (#642).
func TestSubscribeSurvivesConnectionDrop(t *testing.T) {
	t.Parallel()
	for _, disableRetry := range []bool{false, true} {
		t.Run(fmt.Sprintf("DisableRetry=%v", disableRetry), func(t *testing.T) {
			t.Parallel()
			// Own server per subtest: CLIENT KILL TYPE pubsub cuts every subscriber on it.
			client, err := rueidis.NewClient(rueidis.ClientOption{InitAddress: []string{redistest.Addr(t)}, DisableRetry: disableRetry})
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			t.Cleanup(client.Close)
			bus := New(client, slog.New(slog.DiscardHandler))
			var gaps atomic.Int32
			got := make(chan []byte, 1)
			defer bus.SubscribeWithGap("drop.topic", collect(got), func() { gaps.Add(1) })()
			awaitMessage(t, bus, "drop.topic", "before", got)
			if n := gaps.Load(); n != 0 {
				t.Fatalf("gaps before drop = %d, want 0", n)
			}

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			killed, err := client.Do(ctx, client.B().ClientKill().TypePubsub().SkipmeNo().Build()).AsInt64()
			if err != nil || killed < 1 {
				t.Fatalf("CLIENT KILL TYPE pubsub = (%d, %v), want at least one killed connection", killed, err)
			}
			awaitMessage(t, bus, "drop.topic", "after", got)
			awaitCount(t, &gaps, 1)
		})
	}
}

// TestSubscribeCancelUnsubscribes pins that cancel removes the Redis
// subscription and that onGap never fires without a reconnect.
func TestSubscribeCancelUnsubscribes(t *testing.T) {
	t.Parallel()
	client := redistest.Start(t)
	bus := New(client, slog.New(slog.DiscardHandler))
	var gaps atomic.Int32
	got := make(chan []byte, 1)
	cancel := bus.SubscribeWithGap("cancel.topic", collect(got), func() { gaps.Add(1) })
	awaitMessage(t, bus, "cancel.topic", "first", got)
	cancel()

	ctx, done := context.WithTimeout(t.Context(), 10*time.Second)
	defer done()
	const maxPolls = 100
	for range maxPolls {
		reply, err := client.Do(ctx, client.B().PubsubNumsub().Channel("cancel.topic").Build()).ToArray()
		if err != nil || len(reply) != 2 {
			t.Fatalf("PUBSUB NUMSUB = (%v, %v)", reply, err)
		}
		if n, err := reply[1].AsInt64(); err == nil && n == 0 {
			if g := gaps.Load(); g != 0 {
				t.Fatalf("gaps = %d, want 0 without a reconnect", g)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("cancel.topic still has a subscriber after cancel")
}

func TestResubscribePolicyIsBounded(t *testing.T) {
	t.Parallel()
	policy := resubscribePolicy()
	if err := policy.Validate(); err != nil {
		t.Fatalf("resubscribePolicy invalid: %v", err)
	}
	if policy.Max > 30*time.Second || policy.MaxAttempts < 2 {
		t.Fatalf("resubscribePolicy = %+v, want Max <= 30s and at least one retry", policy)
	}
}

// awaitCount waits until counter reaches want, then fails if it overshoots.
func awaitCount(t *testing.T, counter *atomic.Int32, want int32) {
	t.Helper()
	const maxPolls = 100
	for range maxPolls {
		if counter.Load() >= want {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got := counter.Load(); got != want {
		t.Fatalf("count = %d, want %d", got, want)
	}
}

// collect forwards each payload to got without blocking the subscriber.
func collect(got chan<- []byte) pubsub.Handler {
	return func(m pubsub.Message) {
		b, _ := m.Data.([]byte)
		select {
		case got <- b:
		default:
		}
	}
}

// awaitMessage publishes want until the subscriber receives it, because
// SUBSCRIBE and any resubscribe complete asynchronously.
func awaitMessage(t *testing.T, bus *Bus, topic, want string, got <-chan []byte) {
	t.Helper()
	const maxPublishAttempts = 100
	deadline := time.After(10 * time.Second)
	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()
	for range maxPublishAttempts {
		select {
		case b := <-got:
			if string(b) == want {
				return
			}
		case <-tick.C:
			bus.Publish(context.Background(), pubsub.Message{Topic: topic, Data: want})
		case <-deadline:
			t.Fatalf("timed out waiting for %q on %s", want, topic)
		}
	}
	t.Fatalf("no %q after %d publish attempts", want, maxPublishAttempts)
}

func TestTryPublishReportsFailures(t *testing.T) {
	t.Parallel()
	if err := New(nil, nil).TryPublish(t.Context(), pubsub.Message{Topic: "t"}); err == nil {
		t.Fatal("TryPublish without client = nil; want error")
	}
	client := redistest.Start(t)
	bus := New(client, nil)
	if err := bus.TryPublish(t.Context(), pubsub.Message{Topic: "t", Data: make(chan int)}); err == nil {
		t.Fatal("TryPublish of unencodable data = nil; want error")
	}
	if err := bus.TryPublish(t.Context(), pubsub.Message{Topic: "t", Data: "ok"}); err != nil {
		t.Fatalf("TryPublish = %v; want nil", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := bus.TryPublish(ctx, pubsub.Message{Topic: "t", Data: "late"}); err == nil {
		t.Fatal("TryPublish with canceled context = nil; want error")
	}
}
