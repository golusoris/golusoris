// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

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

	"github.com/golusoris/golusoris/testutil/internal/startgate"
)

const (
	defaultImage = "clickhouse/clickhouse-server:24"
	// startTimeout bounds one container start including a cold image pull;
	// same value as testutil/pg (see the rationale there: cold ARC runners).
	startTimeout = 3 * time.Minute
)

// Start boots a ClickHouse container and returns a connected chgo.Conn.
// The container and connection are closed via t.Cleanup.
func Start(t *testing.T) chgo.Conn {
	t.Helper()
	if testing.Short() {
		t.Skip("testutil/clickhouse: container-backed; skipped under -short")
	}

	testcontainers.SkipIfProviderIsNotHealthy(t) // skip cleanly when Docker is unavailable

	// Queue for a boot slot first, so startTimeout only counts the boot itself.
	defer startgate.Acquire(t)()
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

	return openConn(ctx, t, ctr)
}

// openConn resolves the container's native port, opens a chgo connection,
// verifies it with a ping and registers its closing with t.Cleanup.
func openConn(ctx context.Context, t *testing.T, ctr testcontainers.Container) chgo.Conn {
	t.Helper()
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
