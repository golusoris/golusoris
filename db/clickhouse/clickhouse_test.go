package clickhouse_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/golusoris/golusoris/db/clickhouse"
	chtestutil "github.com/golusoris/golusoris/testutil/clickhouse"
)

func TestDB_ExecAndQuery(t *testing.T) {
	t.Parallel()
	conn := chtestutil.Start(t)
	db := clickhouse.New(conn, slog.New(slog.DiscardHandler))

	ctx := context.Background()

	const ddl = `CREATE TABLE IF NOT EXISTS test_events (
		id   UInt64,
		name String
	) ENGINE = MergeTree() ORDER BY id`

	if err := db.Exec(ctx, ddl); err != nil {
		t.Fatalf("Exec DDL: %v", err)
	}
	if err := db.Exec(ctx, "INSERT INTO test_events VALUES (?, ?)", uint64(1), "hello"); err != nil {
		t.Fatalf("Exec INSERT: %v", err)
	}

	rows, err := db.Query(ctx, "SELECT id, name FROM test_events WHERE id = 1")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	defer rows.Close()

	if !rows.Next() {
		t.Fatal("expected at least one row")
	}
	var gotID uint64
	var gotName string
	if err := rows.Scan(&gotID, &gotName); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if gotID != 1 || gotName != "hello" {
		t.Fatalf("row: got (%d, %q), want (1, \"hello\")", gotID, gotName)
	}
}

func TestDB_Exec_wrapsError(t *testing.T) {
	t.Parallel()
	conn := chtestutil.Start(t)
	db := clickhouse.New(conn, slog.New(slog.DiscardHandler))

	err := db.Exec(context.Background(), "SELECT * FROM nonexistent_table_xyz")
	if err == nil {
		t.Fatal("expected error for missing table, got nil")
	}
	const want = "clickhouse: exec:"
	if s := err.Error(); len(s) < len(want) || s[:len(want)] != want {
		t.Fatalf("error %q does not start with %q", s, want)
	}
}

func TestDB_Query_wrapsError(t *testing.T) {
	t.Parallel()
	conn := chtestutil.Start(t)
	db := clickhouse.New(conn, slog.New(slog.DiscardHandler))

	_, err := db.Query(context.Background(), "SELECT * FROM nonexistent_table_xyz")
	if err == nil {
		t.Fatal("expected error for missing table, got nil")
	}
	const want = "clickhouse: query:"
	if s := err.Error(); len(s) < len(want) || s[:len(want)] != want {
		t.Fatalf("error %q does not start with %q", s, want)
	}
}

func TestDB_Conn(t *testing.T) {
	t.Parallel()
	conn := chtestutil.Start(t)
	db := clickhouse.New(conn, slog.New(slog.DiscardHandler))

	if got := db.Conn(); got == nil {
		t.Fatal("Conn() returned nil")
	}
}

func TestModule_exported(t *testing.T) {
	t.Parallel()
	_ = clickhouse.Module
}
