package migrate_test

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/golusoris/golusoris/config"
	dbmigrate "github.com/golusoris/golusoris/db/migrate"
	dbpgx "github.com/golusoris/golusoris/db/pgx"
	"github.com/golusoris/golusoris/log"
)

func TestNewRequiresDSN(t *testing.T) {
	t.Parallel()
	logger := log.New(log.Options{})
	_, err := dbmigrate.New(dbmigrate.Options{}, dbpgx.Options{}, logger)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no DSN") {
		t.Errorf("error %q missing %q", err, "no DSN")
	}
}

func TestNewInheritsPgxDSN(t *testing.T) {
	t.Parallel()
	logger := log.New(log.Options{})
	// Path doesn't exist; we expect the error to be about the source, not the DSN.
	// This proves the DSN was inherited (otherwise we'd get the "no DSN" error).
	_, err := dbmigrate.New(
		dbmigrate.Options{Path: "/nonexistent-migrations-dir-for-test"},
		dbpgx.Options{DSN: "postgres://app:app@127.0.0.1:1/app?sslmode=disable"},
		logger,
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if strings.Contains(err.Error(), "no DSN") {
		t.Errorf("DSN should have been inherited; got %q", err)
	}
}

func TestLoadOptionsFromConfig(t *testing.T) {
	t.Setenv("APP_DB_MIGRATE_PATH", "db/migrations")
	t.Setenv("APP_DB_MIGRATE_AUTO", "true")

	cfg, err := config.New(config.Options{EnvPrefix: "APP_", Delimiter: "."})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	var opts dbmigrate.Options
	if err := cfg.Unmarshal("db.migrate", &opts); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if opts.Path != "db/migrations" {
		t.Errorf("Path = %q", opts.Path)
	}
	if !opts.Auto {
		t.Error("Auto = false, want true")
	}
}

func TestWithFS(t *testing.T) {
	t.Parallel()
	var f fs.FS = fstest.MapFS{}
	o := dbmigrate.Options{Path: "x"}.WithFS(f)
	if o.FS == nil {
		t.Error("FS not set")
	}
	if o.Path != "x" {
		t.Errorf("Path mutated to %q", o.Path)
	}
}
