package bun_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/golusoris/golusoris/config"
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

// TestNewOverSharedPool checks that the bun.DB is wired over the db/pgx pool and
// can both run a raw query (via the embedded *sql.DB) and drive the ORM query
// builder through pgdialect. Skips when Docker is unavailable (pg.Start).
func TestNewOverSharedPool(t *testing.T) {
	t.Parallel()
	pool := pgtest.Start(t)
	db := dbbun.New(pool, dbbun.Options{}, slog.New(slog.DiscardHandler))
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
}
