// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package bun_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/golusoris/golusoris/core/config"
	dbbun "github.com/golusoris/golusoris/db/bun"
	pgtest "github.com/golusoris/golusoris/testutil/pg"
)

func TestOptionsFromConfig(t *testing.T) {
	t.Setenv("APP_DB_BUN_VERBOSE", "true")

	cfg, err := config.New(config.Options{EnvPrefix: "APP_", Delimiter: "."})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	var opts dbbun.Options
	if err := cfg.Unmarshal("db.bun", &opts); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !opts.Verbose {
		t.Error("Verbose = false, want true")
	}
}

func TestNewRejectsNilPool(t *testing.T) {
	t.Parallel()

	db, err := dbbun.New(nil, dbbun.Options{}, slog.New(slog.DiscardHandler))
	if err == nil {
		if db != nil {
			_ = db.Close()
		}
		t.Fatal("New accepted nil pool")
	}
	if db != nil {
		t.Fatal("New returned DB with nil pool")
	}
}

func TestNewRejectsUninitializedPool(t *testing.T) {
	t.Parallel()

	db, err := dbbun.New(new(pgxpool.Pool), dbbun.Options{}, slog.New(slog.DiscardHandler))
	if db != nil {
		_ = db.Close()
		t.Fatal("New returned database for an uninitialized pool")
	}
	if err == nil || !strings.Contains(err.Error(), "invalid pool") {
		t.Fatalf("New error = %v, want invalid pool", err)
	}
}

func TestNewNilLogger(t *testing.T) {
	t.Parallel()

	db, err := dbbun.New(newLazyPool(t), dbbun.Options{}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if db == nil {
		t.Fatal("New returned nil")
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func newLazyPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig("postgres://user:pass@127.0.0.1:1/database?sslmode=disable")
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	config.MinConns = 0
	pool, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatalf("NewWithConfig: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestNewOverSharedPool checks that the bun.DB is wired over the db/pgx pool and
// can both run a raw query (via the embedded *sql.DB) and drive the ORM query
// builder through pgdialect. Skips when Docker is unavailable (pg.Start).
func TestNewOverSharedPool(t *testing.T) {
	t.Parallel()
	pool := pgtest.Start(t)
	db, err := dbbun.New(pool, dbbun.Options{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()

	var raw int
	if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&raw); err != nil {
		t.Fatalf("raw query over shared pool: %v", err)
	}
	if raw != 1 {
		t.Errorf("raw = %d, want 1", raw)
	}

	var built int
	if err := db.NewSelect().ColumnExpr("2 + 2").Scan(ctx, &built); err != nil {
		t.Fatalf("bun query builder: %v", err)
	}
	if built != 4 {
		t.Errorf("built = %d, want 4", built)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close bun adapter: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("closing bun adapter closed shared pool: %v", err)
	}
}
