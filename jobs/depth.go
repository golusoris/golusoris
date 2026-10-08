// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package jobs

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/validate"
)

const (
	defaultTenantTopN   = 20
	defaultMaxQueues    = 64
	defaultDepthTTL     = 10 * time.Second
	defaultDepthTimeout = 2 * time.Second
	// MaxDepthRows bounds one DepthQuerier read; smaller groups past it are dropped.
	MaxDepthRows = 10_000
	// OtherLabel replaces queue/tenant label values beyond the top-N bound.
	OtherLabel = "other"
	// NoTenantLabel marks jobs whose metadata lacks the tenant key.
	NoTenantLabel = "none"
)

// ErrUnknownQueue reports a queue that is neither configured nor present in
// river_job; external scalers map it to NotFound.
var ErrUnknownQueue = errors.New("jobs: unknown queue")

// MetricsOptions tunes [DepthCollector]. Config: jobs.metrics.*.
type MetricsOptions struct {
	// Queues are exported even when empty, beside "default" and
	// jobs.queue.queues; list every queue an external scaler watches.
	Queues []string `koanf:"queues"`
	// TenantKey adds a tenant label to river_jobs from metadata[TenantKey].
	TenantKey string `koanf:"tenant_key"`
	// TenantTopN keeps the N largest tenants as labels; the rest become "other".
	TenantTopN int `koanf:"tenant_top_n"`
	// MaxQueues bounds queue label values the same way (configured queues always kept).
	MaxQueues int `koanf:"max_queues"`
	// CacheTTL reuses one snapshot across scrapes and scaler polls.
	CacheTTL time.Duration `koanf:"cache_ttl"`
	// QueryTimeout bounds each snapshot query.
	QueryTimeout time.Duration `koanf:"query_timeout"`
}

// DefaultMetricsOptions returns 20 tenants, 64 queues, a 10s cache and a 2s query bound.
func DefaultMetricsOptions() MetricsOptions {
	return MetricsOptions{
		TenantTopN:   defaultTenantTopN,
		MaxQueues:    defaultMaxQueues,
		CacheTTL:     defaultDepthTTL,
		QueryTimeout: defaultDepthTimeout,
	}
}

func (o MetricsOptions) withDefaults() MetricsOptions {
	d := DefaultMetricsOptions()
	if o.TenantTopN == 0 {
		o.TenantTopN = d.TenantTopN
	}
	if o.MaxQueues == 0 {
		o.MaxQueues = d.MaxQueues
	}
	if o.CacheTTL == 0 {
		o.CacheTTL = d.CacheTTL
	}
	if o.QueryTimeout == 0 {
		o.QueryTimeout = d.QueryTimeout
	}
	return o
}

func (o MetricsOptions) validate() error {
	if o.TenantTopN < 0 || o.MaxQueues < 0 || o.CacheTTL < 0 || o.QueryTimeout < 0 {
		return errors.New("jobs: metrics: bounds and durations must not be negative")
	}
	return nil
}

// StateCount is one river_job group. OldestAge is now minus the group's
// earliest scheduled_at; it is the backlog age for state "available".
type StateCount struct {
	Queue     string
	State     string
	Tenant    string
	Count     int64
	OldestAge time.Duration
}

// DepthQuerier reads river_job grouped by queue, state and metadata[tenantKey]
// (empty tenant when the key is unset or absent), at most [MaxDepthRows] rows.
type DepthQuerier interface {
	QueryDepth(ctx context.Context, tenantKey string) ([]StateCount, error)
}

// DepthCollector exports queue depth as Prometheus gauges and answers
// per-queue depth for external scalers, from one cached snapshot.
type DepthCollector struct {
	querier DepthQuerier
	opts    MetricsOptions
	known   map[string]bool
	clock   clock.Clock
	logger  *slog.Logger
	descs   depthDescs

	mu      sync.Mutex
	rows    []StateCount
	err     error
	fetched time.Time
	loaded  bool
}

type depthDescs struct {
	available, oldest, jobs, up *prometheus.Desc
}

// NewDepthCollector builds a collector over q. clk drives the cache TTL (nil =
// wall clock).
func NewDepthCollector(q DepthQuerier, opts MetricsOptions, clk clock.Clock, logger *slog.Logger) (*DepthCollector, error) {
	if validate.IsNil(q) {
		return nil, errors.New("jobs: metrics: nil querier")
	}
	if logger == nil {
		return nil, errors.New("jobs: metrics: nil logger")
	}
	if err := opts.validate(); err != nil {
		return nil, err
	}
	opts = opts.withDefaults()
	if validate.IsNil(clk) {
		clk = clockwork.NewRealClock()
	}
	known := map[string]bool{DefaultQueue: true}
	for _, name := range opts.Queues {
		known[name] = true
	}
	return &DepthCollector{
		querier: q, opts: opts, known: known, clock: clk, logger: logger,
		descs: newDepthDescs(opts.TenantKey != ""),
	}, nil
}

func newDepthDescs(withTenant bool) depthDescs {
	jobLabels := []string{"queue", "state"}
	if withTenant {
		jobLabels = append(jobLabels, "tenant")
	}
	return depthDescs{
		available: prometheus.NewDesc("river_queue_available", "Jobs ready to run, per queue.", []string{"queue"}, nil),
		oldest: prometheus.NewDesc("river_queue_oldest_available_age_seconds",
			"Age of the oldest available job, per queue.", []string{"queue"}, nil),
		jobs: prometheus.NewDesc("river_jobs", "Jobs per queue and state (and tenant when configured).", jobLabels, nil),
		up:   prometheus.NewDesc("river_depth_collector_up", "1 when the last depth query succeeded.", nil, nil),
	}
}

// Describe implements prometheus.Collector.
func (c *DepthCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.descs.available
	ch <- c.descs.oldest
	ch <- c.descs.jobs
	ch <- c.descs.up
}

// Collect implements prometheus.Collector. A failed query keeps serving the
// last good snapshot and reports river_depth_collector_up 0.
func (c *DepthCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), c.opts.QueryTimeout)
	defer cancel()
	rows, err := c.snapshot(ctx)
	up := 1.0
	if err != nil {
		up = 0
		c.logger.WarnContext(ctx, "jobs: depth query failed", slog.String("error", err.Error()))
	}
	c.emit(ch, c.descs.up, up)
	v := c.view(rows)
	for queue, n := range v.available {
		c.emit(ch, c.descs.available, float64(n), queue)
		c.emit(ch, c.descs.oldest, v.oldest[queue].Seconds(), queue)
	}
	for k, n := range v.jobs {
		labels := []string{k.queue, k.state}
		if c.opts.TenantKey != "" {
			labels = append(labels, k.tenant)
		}
		c.emit(ch, c.descs.jobs, float64(n), labels...)
	}
}

func (c *DepthCollector) emit(ch chan<- prometheus.Metric, desc *prometheus.Desc, v float64, labels ...string) {
	m, err := prometheus.NewConstMetric(desc, prometheus.GaugeValue, v, labels...)
	if err != nil {
		c.logger.Error("jobs: depth metric", slog.String("error", err.Error()))
		return
	}
	ch <- m
}

// QueueDepth returns outstanding jobs (available + running) in queue, so a
// scaler keeps workers while jobs still run. Unknown queues return
// [ErrUnknownQueue].
func (c *DepthCollector) QueueDepth(ctx context.Context, queue string) (int64, error) {
	rows, err := c.snapshot(ctx)
	if err != nil {
		return 0, err
	}
	seen := c.known[queue]
	var depth int64
	for _, r := range rows {
		if r.Queue != queue {
			continue
		}
		seen = true
		if r.State == string(JobStateAvailable) || r.State == string(JobStateRunning) {
			depth += r.Count
		}
	}
	if !seen {
		return 0, fmt.Errorf("jobs: queue depth %q: %w", queue, ErrUnknownQueue)
	}
	return depth, nil
}

// snapshot returns cached rows younger than CacheTTL, else re-queries. The
// lock is held across the query so concurrent scrapes cost one DB round trip.
func (c *DepthCollector) snapshot(ctx context.Context) ([]StateCount, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.loaded && c.clock.Since(c.fetched) < c.opts.CacheTTL {
		return c.rows, c.err
	}
	qctx, cancel := context.WithTimeout(ctx, c.opts.QueryTimeout)
	defer cancel()
	rows, err := c.querier.QueryDepth(qctx, c.opts.TenantKey)
	c.fetched, c.loaded = c.clock.Now(), true
	if err != nil {
		c.err = fmt.Errorf("jobs: depth query: %w", err)
		return c.rows, c.err
	}
	if len(rows) >= MaxDepthRows {
		c.logger.WarnContext(ctx, "jobs: depth rows truncated", slog.Int("limit", MaxDepthRows))
	}
	c.rows, c.err = rows, nil
	return rows, nil
}

type jobKey struct{ queue, state, tenant string }

type depthView struct {
	available map[string]int64
	oldest    map[string]time.Duration
	jobs      map[jobKey]int64
}

// view folds rows onto bounded label sets: configured queues plus the largest
// others up to MaxQueues, and the TenantTopN largest tenants.
func (c *DepthCollector) view(rows []StateCount) depthView {
	queueTotals, tenantTotals := map[string]int64{}, map[string]int64{}
	for _, r := range rows {
		queueTotals[r.Queue] += r.Count
		tenantTotals[tenantOf(r)] += r.Count
	}
	queues := topLabels(queueTotals, c.known, c.opts.MaxQueues)
	tenants := topLabels(tenantTotals, nil, c.opts.TenantTopN)
	v := depthView{available: map[string]int64{}, oldest: map[string]time.Duration{}, jobs: map[jobKey]int64{}}
	for name := range c.known {
		v.available[name] = 0
	}
	for _, r := range rows {
		q, tenant := foldLabel(queues, r.Queue), foldLabel(tenants, tenantOf(r))
		v.jobs[jobKey{q, r.State, tenant}] += r.Count
		if r.State == string(JobStateAvailable) {
			v.available[q] += r.Count
			v.oldest[q] = max(v.oldest[q], r.OldestAge)
		}
	}
	return v
}

func tenantOf(r StateCount) string {
	if r.Tenant == "" {
		return NoTenantLabel
	}
	return r.Tenant
}

func foldLabel(keep map[string]bool, v string) string {
	if keep[v] {
		return v
	}
	return OtherLabel
}

// topLabels keeps every pinned name plus the largest remaining totals until
// limit names are kept; ties break by name for stable output.
func topLabels(totals map[string]int64, pinned map[string]bool, limit int) map[string]bool {
	keep := make(map[string]bool, limit)
	for name := range pinned {
		keep[name] = true
	}
	names := make([]string, 0, len(totals))
	for name := range totals {
		if !keep[name] {
			names = append(names, name)
		}
	}
	slices.SortFunc(names, func(a, b string) int {
		return cmp.Or(cmp.Compare(totals[b], totals[a]), cmp.Compare(a, b))
	})
	for _, name := range names[:min(len(names), max(limit-len(keep), 0))] {
		keep[name] = true
	}
	return keep
}
