// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package sqlite runs the jobs queue on SQLite through River's riversqlite
// driver, for standalone single-binary deployments. It takes the *sql.DB from
// db/sqlite, shares jobs.Options (config prefix jobs.*), and exposes the same
// driver-agnostic jobs.Inserter as the Postgres module, so app code that only
// enqueues does not care which backend runs.
//
// River's upstream advice for SQLite is one open connection
// (db.SetMaxOpenConns(1)): River runs maintenance in parallel and SQLite
// allows one writer, so a larger pool can surface SQLITE_BUSY.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/riverqueue/river/riverdriver/riversqlite"
	"github.com/riverqueue/river/rivermigrate"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/validate"
	"github.com/golusoris/golusoris/jobs"
)

// migrateTimeout bounds the River schema migration run at fx Start.
const migrateTimeout = 30 * time.Second

// depthSQL mirrors the Postgres depth query: one grouped read, largest groups
// first, bounded by LIMIT. julianday() parses River's text timestamps.
const depthSQL = `SELECT queue, state, COALESCE(json_extract(metadata, ?), ''), count(*),
	COALESCE((julianday('now') - julianday(min(scheduled_at))) * 86400.0, 0)
FROM river_job
GROUP BY 1, 2, 3
ORDER BY 4 DESC
LIMIT ?`

// New builds a River client over db. Like jobs.New, a nil workers registry
// yields an insert-only client.
func New(db *sql.DB, opts jobs.Options, workers *jobs.Workers, logger *slog.Logger) (*jobs.ClientSQL, error) {
	if db == nil {
		return nil, errors.New("jobs/sqlite: nil db")
	}
	c, err := jobs.NewClient(riversqlite.New(db), opts, workers, logger)
	if err != nil {
		return nil, fmt.Errorf("jobs/sqlite: %w", err)
	}
	return c, nil
}

// Migrate applies River's SQLite schema migrations (up). It is idempotent;
// the fx Module runs it at Start before the client starts.
func Migrate(ctx context.Context, db *sql.DB) error {
	if validate.IsNil(ctx) {
		return errors.New("jobs/sqlite: migrate: nil context")
	}
	if db == nil {
		return errors.New("jobs/sqlite: migrate: nil db")
	}
	migrator, err := rivermigrate.New(riversqlite.New(db), &rivermigrate.Config{Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		return fmt.Errorf("jobs/sqlite: migrate: build migrator: %w", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return fmt.Errorf("jobs/sqlite: migrate: apply: %w", err)
	}
	return nil
}

// DepthQuerier reads river_job depth from SQLite for jobs.DepthCollector.
type DepthQuerier struct {
	db *sql.DB
}

// NewDepthQuerier returns a jobs.DepthQuerier over db.
func NewDepthQuerier(db *sql.DB) (*DepthQuerier, error) {
	if db == nil {
		return nil, errors.New("jobs/sqlite: depth: nil db")
	}
	return &DepthQuerier{db: db}, nil
}

// QueryDepth implements jobs.DepthQuerier. Tenant keys containing `"` or `\`
// are rejected because they cannot be quoted in a SQLite JSON path.
func (q *DepthQuerier) QueryDepth(ctx context.Context, tenantKey string) (out []jobs.StateCount, err error) {
	var path sql.NullString
	if tenantKey != "" {
		if strings.ContainsAny(tenantKey, `"\`) {
			return nil, fmt.Errorf("jobs/sqlite: depth: tenant key %q not usable as JSON path", tenantKey)
		}
		path = sql.NullString{String: `$."` + tenantKey + `"`, Valid: true}
	}
	rows, err := q.db.QueryContext(ctx, depthSQL, path, jobs.MaxDepthRows)
	if err != nil {
		return nil, fmt.Errorf("jobs/sqlite: depth query: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			err = errors.Join(err, fmt.Errorf("jobs/sqlite: depth close: %w", cerr))
		}
	}()
	return scanDepth(rows)
}

func scanDepth(rows *sql.Rows) ([]jobs.StateCount, error) {
	out := make([]jobs.StateCount, 0, 16)
	for rows.Next() {
		var r jobs.StateCount
		var ageSeconds float64
		if err := rows.Scan(&r.Queue, &r.State, &r.Tenant, &r.Count, &ageSeconds); err != nil {
			return nil, fmt.Errorf("jobs/sqlite: depth scan: %w", err)
		}
		r.OldestAge = time.Duration(max(ageSeconds, 0) * float64(time.Second))
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("jobs/sqlite: depth rows: %w", err)
	}
	return out, nil
}

// Module provides jobs.Options (jobs.*), *jobs.Workers, *jobs.ClientSQL,
// jobs.Inserter and a jobs.DepthQuerier (for jobs.MetricsModule). Requires
// *sql.DB (db/sqlite), *config.Config and *slog.Logger. At fx Start it
// migrates the River schema, then starts the client (unless producer-only);
// fx Stop drains it like the Postgres module.
var Module = fx.Module(
	"golusoris.jobs.sqlite",
	fx.Provide(jobs.LoadOptions),
	fx.Provide(jobs.NewWorkers),
	fx.Provide(provideClient),
	fx.Provide(jobs.ProvideInserter[*sql.Tx]),
	fx.Provide(fx.Annotate(NewDepthQuerier, fx.As(new(jobs.DepthQuerier)))),
)

type clientParams struct {
	fx.In
	LC      fx.Lifecycle
	DB      *sql.DB
	Opts    jobs.Options
	Workers *jobs.Workers
	Logger  *slog.Logger
	Clock   clock.Clock `optional:"true"`
}

func provideClient(p clientParams) (*jobs.ClientSQL, error) {
	opts := p.Opts
	if !opts.Enabled {
		return nil, nil //nolint:nilnil // documented disabled contract, same as jobs.Module
	}
	if validate.IsNil(opts.Clock) {
		opts.Clock = p.Clock
	}
	workers := p.Workers
	if opts.ProducerOnly {
		workers = nil
	}
	c, err := New(p.DB, opts, workers, p.Logger)
	if err != nil {
		return nil, err
	}
	p.LC.Append(fx.Hook{OnStart: func(ctx context.Context) error {
		mctx, cancel := context.WithTimeout(ctx, migrateTimeout)
		defer cancel()
		return Migrate(mctx, p.DB)
	}})
	if !opts.ProducerOnly {
		jobs.AppendLifecycle(p.LC, c, opts, p.Logger)
	}
	return c, nil
}
