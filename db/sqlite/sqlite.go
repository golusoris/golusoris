// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package sqlite provides an embedded SQLite database as an fx module over
// modernc.org/sqlite — a pure-Go (CGO-free) driver, so binaries stay
// statically linked and cross-compile cleanly. It is the framework's answer
// for local state in CLIs, edge agents, and single-node services; use db/pgx
// for anything multi-writer or networked. Capability key: db.sqlite.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.uber.org/fx"
	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver

	"github.com/golusoris/golusoris/core/config"
)

// DriverName is the database/sql driver registered by modernc.org/sqlite.
const DriverName = "sqlite"

// MemoryPath opens a private in-memory database (tests, throwaway caches).
const MemoryPath = ":memory:"

// ErrMissingPath is returned when no database path is configured.
var ErrMissingPath = errors.New("db/sqlite: path is required")

// Options configures the connection. Zero value is filled by DefaultOptions
// except Path, which must be set.
type Options struct {
	// Path is the database file, or MemoryPath.
	Path string `koanf:"path"`
	// ReadOnly opens the file with mode=ro.
	ReadOnly bool `koanf:"read_only"`
	// BusyTimeout is how long a connection waits on a locked database.
	BusyTimeout time.Duration `koanf:"busy_timeout"`
	// MaxOpenConns bounds the pool. SQLite allows one writer; WAL lets readers
	// proceed concurrently, so a small pool is the sweet spot.
	MaxOpenConns int `koanf:"max_open_conns"`
	// DisableWAL turns off write-ahead logging (journal_mode=WAL), which is on
	// by default because every service reads while it writes. Zero value = on.
	DisableWAL bool `koanf:"disable_wal"`
	// DisableForeignKeys stops enforcing declared foreign keys (on by default;
	// SQLite itself defaults to off). Zero value = enforced.
	DisableForeignKeys bool `koanf:"disable_foreign_keys"`
	// Pragmas are extra `name(value)` pragmas appended to the DSN.
	Pragmas []string `koanf:"pragmas"`
}

// DefaultOptions returns the opinionated defaults: WAL, foreign keys, a 5 s
// busy timeout, and 4 pooled connections. Path is still required.
func DefaultOptions() Options {
	return Options{
		BusyTimeout:  5 * time.Second,
		MaxOpenConns: 4,
	}
}

func (o Options) withDefaults() Options {
	d := DefaultOptions()
	if o.BusyTimeout == 0 {
		o.BusyTimeout = d.BusyTimeout
	}
	if o.MaxOpenConns <= 0 {
		o.MaxOpenConns = d.MaxOpenConns
	}
	return o
}

// DSN renders the modernc.org/sqlite connection string for the options.
func (o Options) DSN() string {
	o = o.withDefaults()
	q := url.Values{}
	if o.ReadOnly {
		q.Add("mode", "ro")
	}
	q.Add("_pragma", "busy_timeout("+strconv.FormatInt(o.BusyTimeout.Milliseconds(), 10)+")")
	if !o.DisableWAL && o.Path != MemoryPath {
		q.Add("_pragma", "journal_mode(WAL)")
	}
	if !o.DisableForeignKeys {
		q.Add("_pragma", "foreign_keys(1)")
	}
	for _, p := range o.Pragmas {
		q.Add("_pragma", p)
	}
	path := o.Path
	if path != MemoryPath {
		path = filepath.ToSlash(path)
	}
	return "file:" + path + "?" + q.Encode()
}

// Open opens and pings the database. The caller owns the returned *sql.DB.
func Open(ctx context.Context, opts Options, logger *slog.Logger) (*sql.DB, error) {
	if strings.TrimSpace(opts.Path) == "" {
		return nil, ErrMissingPath
	}
	opts = opts.withDefaults()
	db, err := sql.Open(DriverName, opts.DSN())
	if err != nil {
		return nil, fmt.Errorf("db/sqlite: open %s: %w", opts.Path, err)
	}
	db.SetMaxOpenConns(opts.MaxOpenConns)
	if opts.Path == MemoryPath {
		// Each pooled connection would otherwise see its own empty database.
		db.SetMaxOpenConns(1)
	}
	pingCtx, cancel := context.WithTimeout(ctx, opts.BusyTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		pingErr := fmt.Errorf("db/sqlite: ping %s: %w", opts.Path, err)
		if cerr := db.Close(); cerr != nil {
			return nil, errors.Join(pingErr, fmt.Errorf("db/sqlite: close %s: %w", opts.Path, cerr))
		}
		return nil, pingErr
	}
	logger.InfoContext(ctx, "db/sqlite: opened", slog.String("path", opts.Path), slog.Bool("wal", !opts.DisableWAL), slog.Bool("read_only", opts.ReadOnly))
	return db, nil
}

// loadOptions unmarshals the "db.sqlite" key on top of DefaultOptions.
func loadOptions(cfg *config.Config) (Options, error) {
	opts := DefaultOptions()
	if err := cfg.Unmarshal("db.sqlite", &opts); err != nil {
		return Options{}, fmt.Errorf("db/sqlite: load options: %w", err)
	}
	opts = opts.withDefaults()
	if strings.TrimSpace(opts.Path) == "" {
		return Options{}, ErrMissingPath
	}
	return opts, nil
}

// Module provides *sql.DB from config (prefix db.sqlite) and closes it on stop.
var Module = fx.Module(
	"golusoris.db.sqlite",
	fx.Provide(loadOptions),
	fx.Provide(func(lc fx.Lifecycle, opts Options, logger *slog.Logger) (*sql.DB, error) {
		db, err := Open(context.Background(), opts, logger)
		if err != nil {
			return nil, err
		}
		lc.Append(fx.Hook{OnStop: func(context.Context) error {
			if err := db.Close(); err != nil {
				return fmt.Errorf("db/sqlite: close: %w", err)
			}
			return nil
		}})
		return db, nil
	}),
)
