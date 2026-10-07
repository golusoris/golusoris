// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package clickhouse

import (
	"context"
	"crypto/tls"
	"log/slog"
	"strings"
	"testing"

	chgo "github.com/ClickHouse/clickhouse-go/v2"
	chdriver "github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

type typedNilConn struct{ chdriver.Conn }

// TestNew_fields verifies that New wires conn and logger into the struct.
// chgo.Open does not dial — it is safe to call without a running server.
func TestNew_fields(t *testing.T) {
	t.Parallel()
	conn, err := chgo.Open(&chgo.Options{Addr: []string{"localhost:9000"}})
	if err != nil {
		t.Fatalf("chgo.Open: %v", err)
	}
	defer conn.Close()

	db := New(conn, slog.Default())
	if db.conn == nil {
		t.Fatal("conn field must not be nil")
	}
	if db.logger == nil {
		t.Fatal("logger field must not be nil")
	}
}

func TestDB_TypedNilConnReturnsError(t *testing.T) {
	t.Parallel()

	var conn *typedNilConn
	db := New(conn, slog.Default())
	if err := db.Exec(context.Background(), "SELECT 1"); err == nil || !strings.Contains(err.Error(), "nil connection") {
		t.Fatalf("Exec: got %v", err)
	}
	if _, err := db.Query(context.Background(), "SELECT 1"); err == nil || !strings.Contains(err.Error(), "nil connection") {
		t.Fatalf("Query: got %v", err)
	}
}

func TestDriverOptionsHonorTLSAndLogger(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)
	opts := driverOptions(Config{
		Addr:     []string{"clickhouse.example:9440"},
		Database: "analytics",
		Username: "reader",
		Password: "secret",
		TLS:      true,
	}, logger)
	if opts.TLS == nil || opts.TLS.MinVersion != tls.VersionTLS12 || opts.TLS.InsecureSkipVerify {
		t.Fatalf("TLS options = %#v, want verified TLS 1.2+", opts.TLS)
	}
	if opts.Logger != logger {
		t.Fatal("driver logger was not wired")
	}
}
