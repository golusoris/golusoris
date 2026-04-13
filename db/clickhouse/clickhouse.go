// Package clickhouse provides an fx-wired ClickHouse client via
// ClickHouse/clickhouse-go/v2.
//
// Usage:
//
//	fx.New(clickhouse.Module) // requires "db.clickhouse.*" koanf config
//
// Config keys (koanf prefix "db.clickhouse"):
//
//	addr:     ["localhost:9000"]
//	database: "default"
//	username: "default"
//	password: ""
//	tls:      false
package clickhouse

import (
	"context"
	"fmt"
	"log/slog"

	chgo "github.com/ClickHouse/clickhouse-go/v2"
	chdriver "github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
)

// Config holds ClickHouse connection settings.
type Config struct {
	Addr     []string `koanf:"addr"`     // e.g. ["localhost:9000"]
	Database string   `koanf:"database"` // default "default"
	Username string   `koanf:"username"` // default "default"
	Password string   `koanf:"password"`
	TLS      bool     `koanf:"tls"`
}

// DB wraps a ClickHouse connection.
type DB struct {
	conn   chgo.Conn
	logger *slog.Logger
}

// Module is the fx module that provides a *DB.
//
//	fx.New(clickhouse.Module)
var Module = fx.Module("golusoris.db.clickhouse",
	fx.Provide(newFromConfig),
)

type params struct {
	fx.In
	Config *config.Config
	Logger *slog.Logger
	LC     fx.Lifecycle
}

func newFromConfig(p params) (*DB, error) {
	var cfg Config
	if err := p.Config.Unmarshal("db.clickhouse", &cfg); err != nil {
		return nil, fmt.Errorf("clickhouse: config: %w", err)
	}
	if len(cfg.Addr) == 0 {
		cfg.Addr = []string{"localhost:9000"}
	}
	if cfg.Database == "" {
		cfg.Database = "default"
	}
	if cfg.Username == "" {
		cfg.Username = "default"
	}

	opts := &chgo.Options{
		Addr: cfg.Addr,
		Auth: chgo.Auth{
			Database: cfg.Database,
			Username: cfg.Username,
			Password: cfg.Password,
		},
	}

	conn, err := chgo.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: open: %w", err)
	}

	db := &DB{conn: conn, logger: p.Logger}

	p.LC.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if err := conn.Ping(ctx); err != nil {
				return fmt.Errorf("clickhouse: ping: %w", err)
			}
			return nil
		},
		OnStop: func(_ context.Context) error {
			return conn.Close() //nolint:wrapcheck // driver error is self-descriptive
		},
	})

	return db, nil
}

// Exec executes a DDL or DML statement.
func (d *DB) Exec(ctx context.Context, query string, args ...any) error {
	if err := d.conn.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("clickhouse: exec: %w", err)
	}
	return nil
}

// Query runs a SELECT and returns rows. The caller must close the returned rows.
func (d *DB) Query(ctx context.Context, query string, args ...any) (chdriver.Rows, error) {
	rows, err := d.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: query: %w", err)
	}
	return rows, nil
}

// Conn returns the underlying chgo.Conn for advanced use (batch inserts, etc.).
func (d *DB) Conn() chgo.Conn { return d.conn }
