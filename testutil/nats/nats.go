// Package nats boots a real NATS container via testcontainers-go and returns
// its URL. Used by integration tests that exercise pubsub/nats behaviour
// (connect, publish, subscribe, JetStream) against a live server.
//
// Usage:
//
//	func TestMyHandler(t *testing.T) {
//	    url := natstestutil.Start(t)
//	    // url is "nats://host:port" pointing at the container
//	}
//
// Each call spins a fresh container — tests are isolated.
// Docker is required (same contract as testutil/pg).
package nats

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	defaultImage = "nats:2-alpine"
	natsPort     = "4222/tcp"
	startTimeout = 60 * time.Second
)

// Start boots a NATS container with JetStream enabled and returns its URL
// ("nats://host:port"). The container is terminated via t.Cleanup.
func Start(t *testing.T) string {
	t.Helper()
	testcontainers.SkipIfProviderIsNotHealthy(t)

	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()

	req := testcontainers.ContainerRequest{
		Image:        defaultImage,
		ExposedPorts: []string{natsPort},
		// -js enables JetStream; pubsub/nats.Client always creates a JS context.
		Cmd:        []string{"-js"},
		WaitingFor: wait.ForLog("Server is ready"),
	}

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("testutil/nats: start container: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		if termErr := ctr.Terminate(stopCtx); termErr != nil {
			t.Logf("testutil/nats: terminate container: %v", termErr)
		}
	})

	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatalf("testutil/nats: host: %v", err)
	}
	port, err := ctr.MappedPort(ctx, natsPort)
	if err != nil {
		t.Fatalf("testutil/nats: mapped port: %v", err)
	}

	return fmt.Sprintf("nats://%s:%s", host, port.Port())
}
