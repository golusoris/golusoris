// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pg_test

import (
	"context"
	"testing"

	"github.com/golusoris/golusoris/testutil/pg"
)

// TestStart proves end-to-end that a real Postgres container boots, accepts a
// connection, and runs SQL. Docker is required (CI: ubuntu-latest has it).
// Not parallel: TestStart and TestDSN each boot their own Postgres container.
// Running them concurrently doubles peak container-create pressure on CI's
// Docker sidecar at the tail of the suite, where it already times out.
//
//nolint:paralleltest // serialised deliberately; see the comment above
func TestStart(t *testing.T) {
	pool := pg.Start(t)
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT 42").Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	if n != 42 {
		t.Errorf("got %d, want 42", n)
	}
}

//nolint:paralleltest // serialised deliberately; see TestStart
func TestDSN(t *testing.T) {
	dsn := pg.DSN(t)
	if dsn == "" {
		t.Fatal("empty DSN")
	}
}
