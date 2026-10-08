// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"testing/fstest"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/core/log"
	dbmigrate "github.com/golusoris/golusoris/db/migrate"
	migratesqlite "github.com/golusoris/golusoris/db/migrate/sqlite"
	dbsqlite "github.com/golusoris/golusoris/db/sqlite"
)

// twoMigrations is an embedded migration set: two tables, each with a down step.
func twoMigrations() fstest.MapFS {
	return fstest.MapFS{
		"migrations/1_widgets.up.sql":   {Data: []byte("CREATE TABLE widgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL);")},
		"migrations/1_widgets.down.sql": {Data: []byte("DROP TABLE widgets;")},
		"migrations/2_gadgets.up.sql":   {Data: []byte("CREATE TABLE gadgets (id INTEGER PRIMARY KEY);")},
		"migrations/2_gadgets.down.sql": {Data: []byte("DROP TABLE gadgets;")},
	}
}

func tempDB(t *testing.T) dbsqlite.Options {
	t.Helper()
	opts := dbsqlite.DefaultOptions()
	opts.Path = filepath.Join(t.TempDir(), "app.db")
	return opts
}

// tableCount counts the named user tables through an independent connection,
// so the assertion does not depend on the migrator's own handle.
func tableCount(t *testing.T, opts dbsqlite.Options, names ...string) int {
	t.Helper()
	db, err := dbsqlite.Open(context.Background(), opts, log.New(log.Options{}))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	n := 0
	for _, name := range names {
		var got string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", name).Scan(&got)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			t.Fatalf("query %s: %v", name, err)
		default:
			n++
		}
	}
	return n
}

func newMigrator(t *testing.T, opts dbsqlite.Options) *dbmigrate.Migrator {
	t.Helper()
	m, err := migratesqlite.New(dbmigrate.Options{}.WithFS(twoMigrations()), opts, log.New(log.Options{}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func TestUpVersionDownForce(t *testing.T) {
	t.Parallel()
	opts := tempDB(t)
	m := newMigrator(t, opts)

	if err := m.Up(); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if got := tableCount(t, opts, "widgets", "gadgets"); got != 2 {
		t.Fatalf("tables after Up = %d, want 2", got)
	}
	v, dirty, err := m.Version()
	if err != nil || v != 2 || dirty {
		t.Fatalf("Version = (%d, %v, %v), want (2, false, nil)", v, dirty, err)
	}
	if err := m.Steps(-1); err != nil {
		t.Fatalf("Steps(-1): %v", err)
	}
	if got := tableCount(t, opts, "gadgets"); got != 0 {
		t.Fatalf("gadgets still present after Steps(-1)")
	}
	if err := m.Force(1); err != nil {
		t.Fatalf("Force: %v", err)
	}
	if v, dirty, _ := m.Version(); v != 1 || dirty {
		t.Fatalf("Version after Force(1) = (%d, %v), want (1, false)", v, dirty)
	}
	if err := m.Down(); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if got := tableCount(t, opts, "widgets"); got != 0 {
		t.Fatalf("widgets still present after Down")
	}
}

func TestUpIsIdempotent(t *testing.T) {
	t.Parallel()
	m := newMigrator(t, tempDB(t))
	if err := m.Up(); err != nil {
		t.Fatalf("first Up: %v", err)
	}
	if err := m.Up(); err != nil {
		t.Fatalf("second Up (no change) must succeed: %v", err)
	}
}

func TestRefusesDatabasesItCannotMigrate(t *testing.T) {
	t.Parallel()
	logger := log.New(log.Options{})
	readOnly := tempDB(t)
	readOnly.ReadOnly = true
	cases := []struct {
		name string
		opts dbsqlite.Options
		want error
	}{
		{"missing path", dbsqlite.Options{}, dbsqlite.ErrMissingPath},
		{"memory", dbsqlite.Options{Path: dbsqlite.MemoryPath}, migratesqlite.ErrMemoryDatabase},
		{"read only", readOnly, migratesqlite.ErrReadOnlyDatabase},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := migratesqlite.New(dbmigrate.Options{}.WithFS(twoMigrations()), tc.opts, logger)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestURLKeepsThePragmasOfDBSQLite(t *testing.T) {
	t.Parallel()
	opts := dbsqlite.DefaultOptions()
	opts.Path = "/var/lib/app/app.db"
	got, err := migratesqlite.URL(opts)
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	want := "sqlite:///var/lib/app/app.db?" + opts.DSN()[len("file:/var/lib/app/app.db?"):]
	if got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
}

func TestModuleRunsUpOnStartWhenAuto(t *testing.T) {
	t.Parallel()
	opts := tempDB(t)
	cfg, err := config.New(config.Options{EnvPrefix: "MIGRATE_SQLITE_TEST_"})
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	app := fxtest.New(t,
		fx.Supply(cfg, log.New(log.Options{}), opts),
		migratesqlite.Module,
		fx.Replace(dbmigrate.Options{Auto: true}.WithFS(twoMigrations())),
		fx.Invoke(func(*dbmigrate.Migrator) {}),
	)
	app.RequireStart()
	app.RequireStop()
	if got := tableCount(t, opts, "widgets", "gadgets"); got != 2 {
		t.Fatalf("tables after fx start with auto = %d, want 2", got)
	}
}
