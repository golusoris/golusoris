// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/validate"
)

// pgDepthSQL groups river_job once; the (state, queue, ...) fetch index makes
// it an index scan, and LIMIT bounds tenant-cardinality blowups.
const pgDepthSQL = `SELECT queue, state::text, COALESCE(metadata->>$1, ''), count(*),
	COALESCE(EXTRACT(EPOCH FROM (now() - min(scheduled_at))), 0)::float8
FROM river_job
GROUP BY 1, 2, 3
ORDER BY 4 DESC
LIMIT $2`

// PgxDepthQuerier reads river_job depth from Postgres.
type PgxDepthQuerier struct {
	pool *pgxpool.Pool
}

// NewPgxDepthQuerier returns a [DepthQuerier] over pool.
func NewPgxDepthQuerier(pool *pgxpool.Pool) (*PgxDepthQuerier, error) {
	if pool == nil {
		return nil, errors.New("jobs: metrics: nil pool")
	}
	return &PgxDepthQuerier{pool: pool}, nil
}

// QueryDepth implements [DepthQuerier].
func (q *PgxDepthQuerier) QueryDepth(ctx context.Context, tenantKey string) ([]StateCount, error) {
	rows, err := q.pool.Query(ctx, pgDepthSQL, tenantKey, MaxDepthRows)
	if err != nil {
		return nil, fmt.Errorf("jobs: depth query: %w", err)
	}
	defer rows.Close()
	out := make([]StateCount, 0, 16)
	for rows.Next() {
		var r StateCount
		var ageSeconds float64
		if err := rows.Scan(&r.Queue, &r.State, &r.Tenant, &r.Count, &ageSeconds); err != nil {
			return nil, fmt.Errorf("jobs: depth scan: %w", err)
		}
		r.OldestAge = secondsToAge(ageSeconds)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("jobs: depth rows: %w", err)
	}
	return out, nil
}

// secondsToAge clamps negative ages (clock skew, future scheduled_at) to zero.
func secondsToAge(s float64) time.Duration {
	return time.Duration(max(s, 0) * float64(time.Second))
}

// MetricsModule provides a *DepthCollector and registers it on the graph's
// *prometheus.Registry (or the default registerer). The querier is the
// graph's DepthQuerier (jobs/sqlite provides one), else Postgres via
// *pgxpool.Pool. Known queues: "default", jobs.queue.queues, jobs.metrics.queues.
var MetricsModule = fx.Module(
	"golusoris.jobs.metrics",
	fx.Provide(provideDepthCollector),
	fx.Invoke(registerDepthCollector),
)

type depthParams struct {
	fx.In
	Opts     Options
	Logger   *slog.Logger
	Querier  DepthQuerier         `optional:"true"`
	Pool     *pgxpool.Pool        `optional:"true"`
	Clock    clock.Clock          `optional:"true"`
	Registry *prometheus.Registry `optional:"true"`
}

func provideDepthCollector(p depthParams) (*DepthCollector, error) {
	q := p.Querier
	if validate.IsNil(q) {
		pq, err := NewPgxDepthQuerier(p.Pool)
		if err != nil {
			return nil, fmt.Errorf("jobs: metrics: need a DepthQuerier or *pgxpool.Pool: %w", err)
		}
		q = pq
	}
	return NewDepthCollector(q, metricsOptionsFor(p.Opts), p.Clock, p.Logger)
}

// metricsOptionsFor adds the configured worker queues to the exported set.
func metricsOptionsFor(o Options) MetricsOptions {
	m := o.Metrics
	queues := make([]string, 0, len(m.Queues)+len(o.Queue.Queues))
	queues = append(queues, m.Queues...)
	for name := range o.Queue.Queues {
		queues = append(queues, name)
	}
	m.Queues = queues
	return m
}

func registerDepthCollector(p depthParams, c *DepthCollector) error {
	reg := prometheus.DefaultRegisterer
	if p.Registry != nil {
		reg = p.Registry
	}
	err := reg.Register(c)
	if are := (prometheus.AlreadyRegisteredError{}); err == nil || errors.As(err, &are) {
		return nil
	}
	return fmt.Errorf("jobs: metrics: register: %w", err)
}
