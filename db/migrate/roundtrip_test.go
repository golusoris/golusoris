// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package migrate_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"testing/fstest"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/golusoris/golusoris/core/log"
	dbmigrate "github.com/golusoris/golusoris/db/migrate"
	dbpgx "github.com/golusoris/golusoris/db/pgx"
	pgtest "github.com/golusoris/golusoris/testutil/pg"
)

// queryTimeout bounds test queries against PostgreSQL container.
const queryTimeout = 5 * time.Second

// openPool connects an independent pgxpool to verify migration state outside migrator.
func openPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping pool: %v", err)
	}
	return pool
}

// tableExists checks whether public.<table_name> exists via to_regclass.
func tableExists(t *testing.T, pool *pgxpool.Pool, table string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()

	var exists bool
	if err := pool.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", "public."+table).Scan(&exists); err != nil {
		t.Fatalf("query table %s existence: %v", table, err)
	}
	return exists
}

// rowCount counts rows in the named table.
func rowCount(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()

	var count int
	switch table {
	case "widgets":
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM widgets").Scan(&count); err != nil {
			t.Fatalf("query widgets count: %v", err)
		}
	default:
		t.Fatalf("unsupported table for rowCount: %s", table)
	}
	return count
}

// assertTable asserts table existence matches want.
func assertTable(t *testing.T, pool *pgxpool.Pool, table string, want bool) {
	t.Helper()
	if got := tableExists(t, pool, table); got != want {
		t.Fatalf("table %s exists = %v, want %v", table, got, want)
	}
}

// assertWidgetsCount asserts widgets table row count matches want.
func assertWidgetsCount(t *testing.T, pool *pgxpool.Pool, want int) {
	t.Helper()
	if got := rowCount(t, pool, "widgets"); got != want {
		t.Fatalf("table widgets count = %d, want %d", got, want)
	}
}

// assertVersion asserts migrator version and dirty flag match expectations.
func assertVersion(t *testing.T, m *dbmigrate.Migrator, want uint, wantDirty bool) {
	t.Helper()
	v, dirty, err := m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		t.Fatalf("Version err = %v, want nil or ErrNilVersion", err)
	}
	if v != want || dirty != wantDirty {
		t.Fatalf("Version = (%d, %v), want (%d, %v)", v, dirty, want, wantDirty)
	}
}

// roundtripFS provides three migrations with matching down definitions.
func roundtripFS() fstest.MapFS {
	return fstest.MapFS{
		"migrations/1_widgets.up.sql":   {Data: []byte("CREATE TABLE widgets (id bigint primary key, name text not null);")},
		"migrations/1_widgets.down.sql": {Data: []byte("DROP TABLE widgets;")},
		"migrations/2_gadgets.up.sql":   {Data: []byte("CREATE TABLE gadgets (id bigint primary key, widget_id bigint references widgets(id));")},
		"migrations/2_gadgets.down.sql": {Data: []byte("DROP TABLE gadgets;")},
		"migrations/3_seed.up.sql":      {Data: []byte("INSERT INTO widgets VALUES (1,'a'),(2,'b');")},
		"migrations/3_seed.down.sql":    {Data: []byte("DELETE FROM widgets WHERE id IN (1,2);")},
	}
}

// TestUpDownUpRoundTrip verifies full up, down step, full down, and subsequent up.
func TestUpDownUpRoundTrip(t *testing.T) {
	t.Parallel()
	dsn := pgtest.DSN(t)
	pool := openPool(t, dsn)

	m, err := dbmigrate.New(
		dbmigrate.Options{FS: roundtripFS()},
		dbpgx.Options{DSN: dsn},
		log.New(log.Options{}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	// Boundary: Steps(-1) at version 0 returns os.ErrNotExist wrapped by db/migrate.
	if stepErr := m.Steps(-1); !errors.Is(stepErr, os.ErrNotExist) {
		t.Fatalf("Steps(-1) at version 0 err = %v, want os.ErrNotExist", stepErr)
	}

	// 1. Up -> v3, both tables, widgets count 2.
	if upErr := m.Up(); upErr != nil {
		t.Fatalf("Up: %v", upErr)
	}
	assertVersion(t, m, 3, false)
	assertTable(t, pool, "widgets", true)
	assertTable(t, pool, "gadgets", true)
	assertWidgetsCount(t, pool, 2)

	// 2. Steps(-1) -> v2, widgets count 0, both tables.
	if stepErr := m.Steps(-1); stepErr != nil {
		t.Fatalf("Steps(-1): %v", stepErr)
	}
	assertVersion(t, m, 2, false)
	assertTable(t, pool, "widgets", true)
	assertTable(t, pool, "gadgets", true)
	assertWidgetsCount(t, pool, 0)

	// Verify Up resumes from intermediate version 2 to 3.
	if upErr := m.Up(); upErr != nil {
		t.Fatalf("Up from v2: %v", upErr)
	}
	assertVersion(t, m, 3, false)
	assertWidgetsCount(t, pool, 2)
	if stepErr := m.Steps(-1); stepErr != nil {
		t.Fatalf("Steps(-1) back to v2: %v", stepErr)
	}
	assertVersion(t, m, 2, false)
	assertWidgetsCount(t, pool, 0)

	// 3. Down -> no version (v0, false, nil), neither table exists.
	if downErr := m.Down(); downErr != nil {
		t.Fatalf("Down: %v", downErr)
	}
	assertVersion(t, m, 0, false)
	assertTable(t, pool, "widgets", false)
	assertTable(t, pool, "gadgets", false)

	// 4. Up again -> v3, both tables, widgets count 2.
	if upErr := m.Up(); upErr != nil {
		t.Fatalf("second Up: %v", upErr)
	}
	assertVersion(t, m, 3, false)
	assertTable(t, pool, "widgets", true)
	assertTable(t, pool, "gadgets", true)
	assertWidgetsCount(t, pool, 2)

	// 5. Up again -> no error (already current), still v3.
	if upErr := m.Up(); upErr != nil {
		t.Fatalf("idempotent Up: %v", upErr)
	}
	assertVersion(t, m, 3, false)
	assertTable(t, pool, "widgets", true)
	assertTable(t, pool, "gadgets", true)
	assertWidgetsCount(t, pool, 2)
}

// TestDownMigrationFailureLeavesDirty verifies failed down migration leaves schema dirty.
func TestDownMigrationFailureLeavesDirty(t *testing.T) {
	t.Parallel()
	dsn := pgtest.DSN(t)

	fsys := fstest.MapFS{
		"migrations/1_base.up.sql":   {Data: []byte("CREATE TABLE t0 (id int);")},
		"migrations/1_base.down.sql": {Data: []byte("DROP TABLE t0;")},
		"migrations/2_t1.up.sql":     {Data: []byte("CREATE TABLE t1 (id int);")},
		"migrations/2_t1.down.sql":   {Data: []byte("DROP TABLE no_such_table;")},
	}
	m, err := dbmigrate.New(
		dbmigrate.Options{FS: fsys},
		dbpgx.Options{DSN: dsn},
		log.New(log.Options{}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	if upErr := m.Up(); upErr != nil {
		t.Fatalf("Up: %v", upErr)
	}
	assertVersion(t, m, 2, false)

	if downErr := m.Down(); downErr == nil {
		t.Fatal("Down with invalid SQL: want error, got nil")
	}
	assertVersion(t, m, 1, true)

	if forceErr := m.Force(1); forceErr != nil {
		t.Fatalf("Force(1): %v", forceErr)
	}
	assertVersion(t, m, 1, false)
}
