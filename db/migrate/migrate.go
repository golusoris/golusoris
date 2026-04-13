// Package migrate wraps golang-migrate v4 with golusoris conventions: an fx
// module that opens a migrate.Migrate against the configured DB and (opt-in)
// runs Up() during fx start. A Migrator handle is also provided so apps can
// trigger migrations from CLI commands.
//
// Sources:
//   - File path (default): set Options.Path to a directory of .sql files.
//   - Embedded fs.FS: pass an [fs.FS] via [WithFS] when constructing Options
//     in code (no koanf path for embed.FS).
//
// Config keys:
//
//	db.migrate.path     # directory of migrations (default "migrations")
//	db.migrate.auto     # run Up() on fx Start (default false)
//	db.migrate.dsn      # DSN; defaults to db.dsn if empty
package migrate

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // registers pgx5 scheme
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
	dbpgx "github.com/golusoris/golusoris/db/pgx"
)

// Options configures the migrator.
type Options struct {
	// Path is a directory of .sql files (file:// source). Ignored when FS is set.
	Path string `koanf:"path"`
	// Auto runs Up() during fx OnStart. Off by default — many apps prefer
	// running migrations as a separate step (init container, CI job).
	Auto bool `koanf:"auto"`
	// DSN overrides db.dsn. Optional — leave empty to inherit.
	DSN string `koanf:"dsn"`

	// FS is an embedded source. Set via [WithFS]; never populated from config.
	FS fs.FS `koanf:"-"`
}

// WithFS returns Options with FS set, leaving Path/Auto/DSN intact. Use this
// in app wiring when migrations are embedded via embed.FS.
func (o Options) WithFS(f fs.FS) Options {
	o.FS = f
	return o
}

// Migrator is a thin wrapper around *migrate.Migrate that adds context-aware
// logging. Apps inject *Migrator and call Up/Down/Steps/Version.
type Migrator struct {
	m      *migrate.Migrate
	logger *slog.Logger
}

// Up runs all pending migrations. ErrNoChange is treated as success.
func (m *Migrator) Up() error {
	if err := m.m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("db/migrate: up: %w", err)
	}
	m.logger.Info("db/migrate: up complete")
	return nil
}

// Down rolls back all migrations. ErrNoChange is treated as success.
func (m *Migrator) Down() error {
	if err := m.m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("db/migrate: down: %w", err)
	}
	m.logger.Info("db/migrate: down complete")
	return nil
}

// Steps applies n migrations (positive: up, negative: down).
func (m *Migrator) Steps(n int) error {
	if err := m.m.Steps(n); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("db/migrate: steps %d: %w", n, err)
	}
	return nil
}

// Version returns (version, dirty, err). version=0 means no migrations applied.
func (m *Migrator) Version() (uint, bool, error) {
	v, dirty, err := m.m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, fmt.Errorf("db/migrate: version: %w", err)
	}
	return v, dirty, nil
}

// Close releases the migrator's resources. Apps using fx.Module don't call
// this — the lifecycle hook handles it.
func (m *Migrator) Close() error {
	src, db := m.m.Close()
	if src != nil {
		return fmt.Errorf("db/migrate: close source: %w", src)
	}
	if db != nil {
		return fmt.Errorf("db/migrate: close db: %w", db)
	}
	return nil
}

// New constructs a Migrator. If opts.FS is set it takes precedence over Path.
// dsn defaults to opts.DSN, falling back to pgxOpts.DSN.
func New(opts Options, pgxOpts dbpgx.Options, logger *slog.Logger) (*Migrator, error) {
	dsn := opts.DSN
	if dsn == "" {
		dsn = pgxOpts.DSN
	}
	if dsn == "" {
		return nil, fmt.Errorf("db/migrate: no DSN (set db.migrate.dsn or db.dsn)")
	}
	dbURL, err := pgxToMigrateURL(dsn)
	if err != nil {
		return nil, err
	}

	var m *migrate.Migrate
	switch {
	case opts.FS != nil:
		path := opts.Path
		if path == "" {
			path = "migrations"
		}
		src, ferr := iofs.New(opts.FS, path)
		if ferr != nil {
			return nil, fmt.Errorf("db/migrate: iofs source: %w", ferr)
		}
		m, err = migrate.NewWithSourceInstance("iofs", src, dbURL)
	default:
		path := opts.Path
		if path == "" {
			path = "migrations"
		}
		m, err = migrate.New("file://"+path, dbURL)
	}
	if err != nil {
		return nil, fmt.Errorf("db/migrate: open: %w", err)
	}

	return &Migrator{m: m, logger: logger}, nil
}

// pgxToMigrateURL rewrites a pgx-style DSN ("postgres://...") into the
// pgx5:// scheme expected by the golang-migrate pgx/v5 driver. Other schemes
// pass through unchanged.
func pgxToMigrateURL(dsn string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("db/migrate: parse DSN: %w", err)
	}
	if u.Scheme == "postgres" || u.Scheme == "postgresql" {
		u.Scheme = "pgx5"
	}
	return u.String(), nil
}

// loadOptions unmarshals "db.migrate" into Options.
func loadOptions(cfg *config.Config) (Options, error) {
	var opts Options
	if err := cfg.Unmarshal("db.migrate", &opts); err != nil {
		return Options{}, fmt.Errorf("db/migrate: load options: %w", err)
	}
	return opts, nil
}

// Module provides a *Migrator and (when db.migrate.auto=true) runs Up() on
// fx Start. Requires [config.Module], [log.Module], and [dbpgx.Module] in the
// same fx graph.
//
// Apps embedding migrations should override Options via fx.Replace:
//
//	fx.Replace(migrate.Options{Auto: true}.WithFS(myEmbedFS))
var Module = fx.Module("golusoris.db.migrate",
	fx.Provide(loadOptions),
	fx.Provide(func(lc fx.Lifecycle, opts Options, pgxOpts dbpgx.Options, logger *slog.Logger) (*Migrator, error) {
		m, err := New(opts, pgxOpts, logger)
		if err != nil {
			return nil, err
		}
		lc.Append(fx.Hook{
			OnStart: func(_ context.Context) error {
				if !opts.Auto {
					return nil
				}
				return m.Up()
			},
			OnStop: func(_ context.Context) error {
				_ = m.Close() // best-effort; closing migrate also closes its DB
				return nil
			},
		})
		return m, nil
	}),
)
