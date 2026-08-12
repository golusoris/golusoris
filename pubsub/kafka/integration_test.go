package kafka_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/golusoris/golusoris/pubsub/kafka"
	kafkatest "github.com/golusoris/golusoris/testutil/kafka"
)

// newTestClient creates a kafka.Client connected to the given broker address.
// It registers a t.Cleanup to close the underlying kgo.Client.
// When group is non-empty the client is configured with ConsumeResetOffset
// earliest so new groups start from the first available offset.
func newTestClient(t *testing.T, broker string, group string) *kafka.Client {
	t.Helper()

	opts := []kgo.Opt{kgo.SeedBrokers(broker)}
	if group != "" {
		opts = append(opts,
			kgo.ConsumerGroup(group),
			// New consumer groups have no committed offset; start from the
			// earliest available record so tests that produce-then-consume
			// don't miss messages due to the "latest" default.
			kgo.ConsumeResetOffset(kgo.NewOffsetStart()),
		)
	}
	kc, err := kgo.NewClient(opts...)
	if err != nil {
		t.Fatalf("kgo.NewClient: %v", err)
	}
	t.Cleanup(kc.Close)

	// Wrap via exported Kgo accessor — we construct via kgo directly so that
	// testutil/kafka's container address is wired without the full fx stack.
	return kafka.ClientFromKgo(kc)
}

// uniqueTopic returns a topic name derived from the test name to avoid
// cross-test pollution. The name is sanitised for Kafka topic naming rules.
func uniqueTopic(t *testing.T) string {
	t.Helper()
	// Replace characters invalid in Kafka topic names with dashes.
	name := strings.NewReplacer("/", "-", " ", "-", "#", "").Replace(t.Name())
	return fmt.Sprintf("t-%s", name)
}

func TestIntegration_ProduceAndPoll(t *testing.T) {
	t.Parallel()
	addr := kafkatest.Addr(t)

	tests := []struct {
		name    string
		records []*kafka.Record
	}{
		{
			name: "single record",
			records: []*kafka.Record{
				kafka.NewRecord("orders", []byte("k1"), []byte("v1")),
			},
		},
		{
			name: "multiple records",
			records: []*kafka.Record{
				kafka.NewRecord("payments", []byte("k1"), []byte("p1")),
				kafka.NewRecord("payments", []byte("k2"), []byte("p2")),
				kafka.NewRecord("payments", []byte("k3"), []byte("p3")),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			topic := uniqueTopic(t)
			for _, r := range tc.records {
				r.Topic = topic
			}

			producer := newTestClient(t, addr, "")
			consumer := newTestClient(t, addr, "group-"+topic)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			if err := producer.Produce(ctx, tc.records...); err != nil {
				t.Fatalf("Produce: %v", err)
			}

			consumer.Subscribe(topic)

			got := make([]*kafka.Record, 0, len(tc.records))
			for len(got) < len(tc.records) {
				batch, err := consumer.Poll(ctx, len(tc.records))
				if err != nil {
					if errors.Is(err, context.DeadlineExceeded) {
						t.Fatalf("Poll timed out after %d/%d records", len(got), len(tc.records))
					}
					t.Fatalf("Poll: %v", err)
				}
				got = append(got, batch...)
			}

			if len(got) != len(tc.records) {
				t.Fatalf("record count: got %d, want %d", len(got), len(tc.records))
			}

			if err := consumer.CommitOffsets(ctx); err != nil {
				t.Fatalf("CommitOffsets: %v", err)
			}
		})
	}
}

func TestIntegration_Produce_InvalidBroker(t *testing.T) {
	t.Parallel()

	kc, err := kgo.NewClient(kgo.SeedBrokers("127.0.0.1:1")) // port 1 is always closed
	if err != nil {
		t.Fatalf("kgo.NewClient: %v", err)
	}
	t.Cleanup(kc.Close)
	client := kafka.ClientFromKgo(kc)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Producing to an unreachable broker must surface a wrapped error.
	err = client.Produce(ctx, kafka.NewRecord("orders", []byte("k"), []byte("v")))
	if err == nil {
		t.Fatal("expected error for unreachable broker, got nil")
	}
	// kafka.Client.Produce wraps the underlying error — unwrap must return non-nil.
	if errors.Unwrap(err) == nil {
		t.Fatalf("expected wrapped error, got bare: %v", err)
	}
}

func TestIntegration_Poll_ShortTimeout(t *testing.T) {
	t.Parallel()
	addr := kafkatest.Addr(t)

	client := newTestClient(t, addr, "group-poll-short-timeout")
	client.Subscribe("no-messages-here")

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// With no messages on the topic, Poll should return either empty records or
	// a context-deadline error — both are valid outcomes.
	records, err := client.Poll(ctx, 10)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = records
}

func TestIntegration_CommitOffsets_NoGroup(t *testing.T) {
	t.Parallel()
	addr := kafkatest.Addr(t)

	// Without a consumer group, CommitOffsets is a no-op in franz-go;
	// it must not return an error.
	client := newTestClient(t, addr, "")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.CommitOffsets(ctx); err != nil {
		t.Fatalf("CommitOffsets without group: %v", err)
	}
}
