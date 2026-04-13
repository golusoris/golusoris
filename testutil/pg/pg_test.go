package pg_test

import (
	"context"
	"testing"

	"github.com/golusoris/golusoris/testutil/pg"
)

// TestStart proves end-to-end that a real Postgres container boots, accepts a
// connection, and runs SQL. Docker is required (CI: ubuntu-latest has it).
func TestStart(t *testing.T) {
	t.Parallel()
	pool := pg.Start(t)
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT 42").Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	if n != 42 {
		t.Errorf("got %d, want 42", n)
	}
}

func TestDSN(t *testing.T) {
	t.Parallel()
	dsn := pg.DSN(t)
	if dsn == "" {
		t.Fatal("empty DSN")
	}
}
