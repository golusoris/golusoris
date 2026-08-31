// Package kafka boots a Kafka-compatible broker via testcontainers-go and
// returns a broker address suitable for use with twmb/franz-go. Backed by
// Redpanda, which is Kafka-API-compatible and requires no ZooKeeper.
//
// Usage:
//
//	func TestMyHandler(t *testing.T) {
//	    addr := kafkatest.Addr(t)
//	    // addr is "host:port" — pass to kgo.SeedBrokers
//	}
//
// Each call spins a fresh container — tests are isolated.
// Docker is required (testutil/pg contract).
package kafka

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	redpandacontainer "github.com/testcontainers/testcontainers-go/modules/redpanda"
)

const (
	// redpandaImage is Kafka-API-compatible without ZooKeeper.
	redpandaImage = "redpandadata/redpanda:v24.3.1"
	startTimeout  = 90 * time.Second
)

// Addr boots a Redpanda container and returns its Kafka-compatible broker
// address as "host:port". The container is terminated via t.Cleanup.
func Addr(t *testing.T) string {
	t.Helper()
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()

	// The module renders Redpanda's advertised listener with Docker's mapped
	// host port. A hand-written localhost:9092 advertisement breaks as soon as
	// Docker assigns an ephemeral port (and races when tests run in parallel).
	ctr, err := redpandacontainer.Run(ctx, redpandaImage, redpandacontainer.WithAutoCreateTopics())
	if err != nil {
		t.Fatalf("testutil/kafka: start container: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		_ = ctr.Terminate(stopCtx)
	})

	broker, err := ctr.KafkaSeedBroker(ctx)
	if err != nil {
		t.Fatalf("testutil/kafka: get seed broker: %v", err)
	}

	return broker
}
