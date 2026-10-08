// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

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

	"github.com/golusoris/golusoris/internal/testimages"
	"github.com/golusoris/golusoris/testutil/internal/startgate"
)

const (
	// startTimeout bounds one container start including a cold image pull;
	// same value as testutil/pg (see the rationale there: cold CI runners).
	startTimeout = 3 * time.Minute
)

// Addr boots a Redpanda container and returns its Kafka-compatible broker
// address as "host:port". The container is terminated via t.Cleanup.
func Addr(t *testing.T) string {
	t.Helper()
	return start(t)
}

// AddrSASL boots a Redpanda container whose Kafka listener requires SASL
// SCRAM-SHA-256 and creates user with password as a superuser. It returns the
// broker address as "host:port"; the container is terminated via t.Cleanup.
func AddrSASL(t *testing.T, user, password string) string {
	t.Helper()
	return start(t,
		redpandacontainer.WithEnableSASL(),
		redpandacontainer.WithNewServiceAccount(user, password),
		redpandacontainer.WithSuperusers(user),
	)
}

func start(t *testing.T, extra ...testcontainers.ContainerCustomizer) string {
	t.Helper()
	if testing.Short() {
		t.Skip("testutil/kafka: container-backed; skipped under -short")
	}
	testcontainers.SkipIfProviderIsNotHealthy(t)

	// Queue for a boot slot first, so startTimeout only counts the boot itself.
	defer startgate.Acquire(t)()
	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()

	// The module renders Redpanda's advertised listener with Docker's mapped
	// host port. A hand-written localhost:9092 advertisement breaks as soon as
	// Docker assigns an ephemeral port (and races when tests run in parallel).
	opts := append([]testcontainers.ContainerCustomizer{
		redpandacontainer.WithAutoCreateTopics(),
		testimages.WithPinnedReaper(),
	}, extra...)
	ctr, err := redpandacontainer.Run(ctx, testimages.Redpanda, opts...)
	if err != nil {
		t.Fatalf("testutil/kafka: start container: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		if terr := ctr.Terminate(stopCtx); terr != nil {
			t.Logf("testutil/kafka: terminate container: %v", terr)
		}
	})

	broker, err := ctr.KafkaSeedBroker(ctx)
	if err != nil {
		t.Fatalf("testutil/kafka: get seed broker: %v", err)
	}

	return broker
}
