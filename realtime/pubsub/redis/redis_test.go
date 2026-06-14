package redis

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/golusoris/golusoris/realtime/pubsub"
	redistest "github.com/golusoris/golusoris/testutil/redis"
)

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

// TestPubSubRoundTrip publishes to a topic and asserts a subscriber on the same
// bus receives it, across a real Redis (testcontainers; requires Docker).
func TestPubSubRoundTrip(t *testing.T) {
	t.Parallel()
	client := redistest.Start(t)
	bus := New(client, slog.New(slog.DiscardHandler))

	got := make(chan []byte, 1)
	cancel := bus.Subscribe("test.topic", func(m pubsub.Message) {
		b, _ := m.Data.([]byte)
		select {
		case got <- b:
		default:
		}
	})
	defer cancel()

	// SUBSCRIBE is established asynchronously, so retry the publish until the
	// subscriber receives it (or we time out).
	deadline := time.After(10 * time.Second)
	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case b := <-got:
			if string(b) != "hello" {
				t.Fatalf("received %q, want %q", b, "hello")
			}
			return
		case <-tick.C:
			bus.Publish(context.Background(), pubsub.Message{Topic: "test.topic", Data: "hello"})
		case <-deadline:
			t.Fatal("timed out waiting for published message")
		}
	}
}
