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
	"fmt"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	// redpandaImage is Kafka-API-compatible without ZooKeeper.
	redpandaImage = "redpandadata/redpanda:v24.3.1"
	kafkaPort     = "9092/tcp"
	startTimeout  = 90 * time.Second
)

// Addr boots a Redpanda container and returns its Kafka-compatible broker
// address as "host:port". The container is terminated via t.Cleanup.
func Addr(t *testing.T) string {
	t.Helper()
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()

	req := testcontainers.ContainerRequest{
		Image: redpandaImage,
		Cmd: []string{
			"redpanda", "start",
			"--mode", "dev-container",
			"--smp", "1",
			"--memory", "512M",
			"--overprovisioned",
			"--kafka-addr", "0.0.0.0:9092",
			"--advertise-kafka-addr", "localhost:9092",
		},
		ExposedPorts: []string{kafkaPort},
		WaitingFor:   wait.ForLog("Successfully started Redpanda!").WithStartupTimeout(startTimeout),
	}

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("testutil/kafka: start container: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		_ = ctr.Terminate(stopCtx)
	})

	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatalf("testutil/kafka: get host: %v", err)
	}
	port, err := ctr.MappedPort(ctx, kafkaPort)
	if err != nil {
		t.Fatalf("testutil/kafka: get port: %v", err)
	}

	return fmt.Sprintf("%s:%s", host, port.Port())
}
