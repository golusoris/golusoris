package kafka_test

import (
	"testing"

	"github.com/golusoris/golusoris/pubsub/kafka"
)

func TestNewRecord(t *testing.T) {
	t.Parallel()
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
	// Timestamp is intentionally zero — broker assigns it on receipt.
	if !r.Timestamp.IsZero() {
		t.Fatalf("timestamp: expected zero (broker-assigned), got %v", r.Timestamp)
	}
}
