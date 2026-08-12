package clickhouse

import (
	"log/slog"
	"testing"

	chgo "github.com/ClickHouse/clickhouse-go/v2"
)

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
