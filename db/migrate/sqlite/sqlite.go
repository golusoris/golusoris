// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package sqlite runs db/migrate migrations against a SQLite database through
// golang-migrate's pure-Go sqlite driver (modernc.org/sqlite, the driver
// db/sqlite uses). It is a separate package so that PostgreSQL-only programs
// that import db/migrate do not link a SQLite driver.
//
// The migrator opens its own connection with the same pragmas as db/sqlite
// (busy timeout, WAL, foreign keys, extra pragmas) and closes it with the
// Migrator. Config keys are those of db/migrate (db.migrate.path,
// db.migrate.auto) and db/sqlite (db.sqlite.*); db.migrate.dsn is not read.
package sqlite

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	_ "github.com/golang-migrate/migrate/v4/database/sqlite" // registers the sqlite:// scheme
	"go.uber.org/fx"

	dbmigrate "github.com/golusoris/golusoris/db/migrate"
	dbsqlite "github.com/golusoris/golusoris/db/sqlite"
)

// ErrMemoryDatabase is returned for db/sqlite's MemoryPath: the migrator's own
// connection would migrate a private in-memory database nobody else sees.
var ErrMemoryDatabase = errors.New("db/migrate/sqlite: cannot migrate an in-memory database")

// ErrReadOnlyDatabase is returned when the database is opened read-only.
var ErrReadOnlyDatabase = errors.New("db/migrate/sqlite: cannot migrate a read-only database")

// URL renders the golang-migrate database URL for opts: sqlite:// followed by
// the path and the query of db/sqlite's DSN, so both connections share their
// pragmas.
func URL(opts dbsqlite.Options) (string, error) {
	switch {
	case strings.TrimSpace(opts.Path) == "":
		return "", dbsqlite.ErrMissingPath
	case opts.Path == dbsqlite.MemoryPath:
		return "", ErrMemoryDatabase
	case opts.ReadOnly:
		return "", ErrReadOnlyDatabase
	}
	return "sqlite://" + strings.TrimPrefix(opts.DSN(), "file:"), nil
}

// New constructs a Migrator for the SQLite database of sqliteOpts. If opts.FS
// is set it takes precedence over opts.Path.
func New(opts dbmigrate.Options, sqliteOpts dbsqlite.Options, logger *slog.Logger) (*dbmigrate.Migrator, error) {
	dbURL, err := URL(sqliteOpts)
	if err != nil {
		return nil, err
	}
	m, err := dbmigrate.Open(opts, dbURL, logger)
	if err != nil {
		return nil, fmt.Errorf("db/migrate/sqlite: %s: %w", sqliteOpts.Path, err)
	}
	return m, nil
}

// Module provides a *dbmigrate.Migrator for SQLite and (when
// db.migrate.auto=true) runs Up() on fx Start. Requires the db/sqlite Options
// (db/sqlite.Module provides them), a *config.Config and a *slog.Logger. Use
// it instead of db/migrate.Module, not next to it.
var Module = fx.Module(
	"golusoris.db.migrate.sqlite",
	fx.Provide(dbmigrate.LoadOptions),
	fx.Provide(func(lc fx.Lifecycle, opts dbmigrate.Options, sqliteOpts dbsqlite.Options, logger *slog.Logger) (*dbmigrate.Migrator, error) {
		m, err := New(opts, sqliteOpts, logger)
		if err != nil {
			return nil, err
		}
		dbmigrate.Bind(lc, m, opts, logger)
		return m, nil
	}),
)
