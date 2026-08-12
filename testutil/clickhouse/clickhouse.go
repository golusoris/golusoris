// Package clickhouse boots a real ClickHouse container via testcontainers-go
// and returns a connected [chgo.Conn]. Used by tests that need to exercise
// actual ClickHouse behaviour (DDL, queries, batch inserts).
//
// Usage:
//
//	func TestQuery(t *testing.T) {
//	    conn := chtestutil.Start(t)
//	    // ... run tests against conn ...
//	}
//
// Each call spins a fresh container — tests are isolated.
// Docker is required (testutil/pg contract).
package clickhouse

import (
	"context"
	"fmt"
	"testing"
	"time"

	chgo "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	defaultImage = "clickhouse/clickhouse-server:24"
	startTimeout = 90 * time.Second
)

// Start boots a ClickHouse container and returns a connected chgo.Conn.
// The container and connection are closed via t.Cleanup.
func Start(t *testing.T) chgo.Conn {
	t.Helper()
	testcontainers.SkipIfProviderIsNotHealthy(t) // skip cleanly when Docker is unavailable

	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        defaultImage,
			ExposedPorts: []string{"9000/tcp", "8123/tcp"},
			// /ping returns "Ok." when the server is ready.
			WaitingFor: wait.ForHTTP("/ping").WithPort("8123/tcp").WithStartupTimeout(startTimeout),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("testutil/clickhouse: start container: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer stopCancel()
		if termErr := ctr.Terminate(stopCtx); termErr != nil {
			t.Logf("testutil/clickhouse: terminate container: %v", termErr)
		}
	})

	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatalf("testutil/clickhouse: get host: %v", err)
	}
	mappedPort, err := ctr.MappedPort(ctx, "9000/tcp")
	if err != nil {
		t.Fatalf("testutil/clickhouse: get port: %v", err)
	}

	conn, err := chgo.Open(&chgo.Options{
		Addr: []string{fmt.Sprintf("%s:%s", host, mappedPort.Port())},
		Auth: chgo.Auth{
			Database: "default",
			Username: "default",
		},
	})
	if err != nil {
		t.Fatalf("testutil/clickhouse: open connection: %v", err)
	}

	if err := conn.Ping(ctx); err != nil {
		t.Fatalf("testutil/clickhouse: ping: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Logf("testutil/clickhouse: close connection: %v", closeErr)
		}
	})

	return conn
}
