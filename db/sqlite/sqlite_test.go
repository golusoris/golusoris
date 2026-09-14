// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package sqlite_test

import (
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/db/sqlite"
)

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestOpenFileWALRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := sqlite.Open(t.Context(), sqlite.Options{Path: path}, discard())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var mode string
	if err := db.QueryRowContext(t.Context(), "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
	var fk int
	if err := db.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Fatalf("foreign_keys = %d, want 1", fk)
	}
	if _, err := db.ExecContext(t.Context(), "CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "INSERT INTO t (name) VALUES (?), (?)", "a", "b"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM t").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("database file not created: %v", err)
	}
}

func TestOpenMemoryAndReadOnly(t *testing.T) {
	t.Parallel()
	mem, err := sqlite.Open(t.Context(), sqlite.Options{Path: sqlite.MemoryPath}, discard())
	if err != nil {
		t.Fatalf("Open memory: %v", err)
	}
	t.Cleanup(func() { _ = mem.Close() })
	if _, err := mem.ExecContext(t.Context(), "CREATE TABLE m (x INT)"); err != nil {
		t.Fatal(err)
	}
	// A second statement must see the table: the pool is pinned to one conn.
	if _, err := mem.ExecContext(t.Context(), "INSERT INTO m VALUES (1)"); err != nil {
		t.Fatalf("second statement lost the in-memory database: %v", err)
	}

	path := filepath.Join(t.TempDir(), "ro.db")
	rw, err := sqlite.Open(t.Context(), sqlite.Options{Path: path, DisableWAL: true}, discard())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rw.ExecContext(t.Context(), "CREATE TABLE r (x INT)"); err != nil {
		t.Fatal(err)
	}
	if err := rw.Close(); err != nil {
		t.Fatal(err)
	}
	ro, err := sqlite.Open(t.Context(), sqlite.Options{Path: path, ReadOnly: true, DisableWAL: true}, discard())
	if err != nil {
		t.Fatalf("Open read-only: %v", err)
	}
	t.Cleanup(func() { _ = ro.Close() })
	if _, err := ro.ExecContext(t.Context(), "INSERT INTO r VALUES (1)"); err == nil {
		t.Fatal("write on a read-only database must fail")
	}
}

func TestOpenErrors(t *testing.T) {
	t.Parallel()
	if _, err := sqlite.Open(t.Context(), sqlite.Options{}, discard()); !errors.Is(err, sqlite.ErrMissingPath) {
		t.Fatalf("empty path: got %v", err)
	}
	missingDir := filepath.Join(t.TempDir(), "nope", "x.db")
	if _, err := sqlite.Open(t.Context(), sqlite.Options{Path: missingDir}, discard()); err == nil {
		t.Fatal("expected error when the parent directory does not exist")
	}
}

func TestDSN(t *testing.T) {
	t.Parallel()
	dsn := sqlite.Options{Path: "/tmp/x.db", ReadOnly: true, Pragmas: []string{"synchronous(NORMAL)"}}.DSN()
	for _, want := range []string{"file:/tmp/x.db?", "mode=ro", "busy_timeout%285000%29", "journal_mode%28WAL%29", "foreign_keys%281%29", "synchronous%28NORMAL%29"} {
		if !strings.Contains(dsn, want) {
			t.Errorf("DSN %q missing %q", dsn, want)
		}
	}
	if strings.Contains(sqlite.Options{Path: sqlite.MemoryPath}.DSN(), "journal_mode") {
		t.Error("WAL must not be requested for :memory:")
	}
	off := sqlite.Options{Path: "/tmp/y.db", DisableWAL: true, DisableForeignKeys: true}.DSN()
	if strings.Contains(off, "journal_mode") || strings.Contains(off, "foreign_keys") {
		t.Errorf("disabled pragmas leaked into DSN %q", off)
	}
}

func TestModule(t *testing.T) {
	t.Parallel()
	cfgPath := filepath.Join(t.TempDir(), "app.yaml")
	dbPath := filepath.ToSlash(filepath.Join(t.TempDir(), "mod.db"))
	if err := os.WriteFile(cfgPath, []byte("db:\n  sqlite:\n    path: "+dbPath+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.New(config.Options{Files: []string{cfgPath}})
	if err != nil {
		t.Fatal(err)
	}
	var db *sql.DB
	app := fxtest.New(t, fx.Supply(cfg, discard()), sqlite.Module, fx.Populate(&db))
	app.RequireStart()
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("ping via module: %v", err)
	}
	app.RequireStop()
	if err := db.PingContext(t.Context()); err == nil {
		t.Fatal("database must be closed after fx stop")
	}

	emptyCfg, err := config.New(config.Options{Files: nil})
	if err != nil {
		t.Fatal(err)
	}
	bad := fx.New(fx.Supply(emptyCfg, discard()), sqlite.Module, fx.Populate(&db), fx.NopLogger)
	if !errors.Is(bad.Err(), sqlite.ErrMissingPath) {
		t.Fatalf("expected ErrMissingPath from fx construction, got %v", bad.Err())
	}
}
