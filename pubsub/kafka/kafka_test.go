package kafka_test

import (
	"testing"
	"time"

	"github.com/golusoris/golusoris/pubsub/kafka"
)

func TestNewRecord(t *testing.T) {
	r := kafka.NewRecord("orders", []byte("key"), []byte("value"))
	if r.Topic != "orders" {
		t.Fatalf("topic: got %q, want %q", r.Topic, "orders")
	}
	if string(r.Key) != "key" {
		t.Fatalf("key: got %q, want %q", r.Key, "key")
	}
	if string(r.Value) != "value" {
		t.Fatalf("value: got %q, want %q", r.Value, "value")
	}
	if r.Timestamp.IsZero() {
		t.Fatal("timestamp should not be zero")
	}
	if time.Since(r.Timestamp) > 5*time.Second {
		t.Fatal("timestamp too old")
	}
}
