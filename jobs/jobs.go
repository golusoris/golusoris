// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package jobs wires a [river] client as an fx dependency so apps can
// enqueue + process background jobs backed by Postgres.
//
// Workers register via `jobs.Register[T](c, worker)`. The client starts
// during fx Start (when queues are configured) and drains gracefully on
// Stop. Insert-only clients (no queues/workers) are also supported for
// producer-only apps.
//
// Config keys (env: APP_JOBS_*):
//
//	jobs.enabled              # master switch (default true)
//	jobs.producer_only        # enqueue jobs without starting workers
//	jobs.queue.default.max    # max concurrent workers on the default queue (default 10)
//	jobs.job.timeout          # per-job timeout (default 30s; workers can override)
//	jobs.job.max_attempts     # default max attempts (default 25 = ~3 days retries)
//	jobs.fetch_cooldown       # pg LISTEN cooldown (default 100ms)
//	jobs.rescue_stuck_after   # rescue jobs stuck running for this long (default 1h)
//	jobs.stop.soft            # graceful drain before job contexts are cancelled (default 10s)
//	jobs.stop.hard            # wait for cancelled jobs after the soft phase (default 5s)
//	jobs.retry.base           # exponential backoff base; 0 keeps River's attempt^4 policy
//	jobs.retry.max            # backoff cap (default 1h when base is set)
//	jobs.retry.jitter         # +/- fraction applied to each delay (0..1)
//	jobs.tracing.enabled      # OpenTelemetry insert/work spans via otelriver (default false)
//	jobs.tracing.propagate    # carry trace context in job metadata (default true)
//	jobs.metrics.*            # queue-depth collector, see MetricsOptions and MetricsModule
//
// See [river]'s docs for full Config reference.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/core/validate"
)

// Options tunes the river client.
type Options struct {
	Enabled bool `koanf:"enabled"`
	// ProducerOnly builds an enqueue-only client and skips River's worker
	// lifecycle. Use this when another service processes the queues.
	ProducerOnly  bool          `koanf:"producer_only"`
	Queue         QueueOptions  `koanf:"queue"`
	Job           JobOptions    `koanf:"job"`
	FetchCooldown time.Duration `koanf:"fetch_cooldown"`
	// FetchPollInterval is the backstop poll between LISTEN/NOTIFY fetches
	// (0 = river default). Pair with FetchCooldown for latency tuning.
	FetchPollInterval time.Duration `koanf:"fetch_poll_interval"`
	RescueStuckAfter  time.Duration `koanf:"rescue_stuck_after"`
	// CompletedJobRetention / DiscardedJobRetention bound how long terminal
	// jobs stay in the table before river prunes them (0 = river default).
	CompletedJobRetention time.Duration `koanf:"completed_job_retention"`
	DiscardedJobRetention time.Duration `koanf:"discarded_job_retention"`
	// Stop bounds the two-phase drain on fx Stop (see [Drain]).
	Stop StopOptions `koanf:"stop"`
	// Retry replaces River's default backoff when Retry.Base > 0.
	Retry RetryOptions `koanf:"retry"`
	// Tracing adds OpenTelemetry spans for insert and work.
	Tracing TracingOptions `koanf:"tracing"`
	// Metrics tunes the queue-depth collector built by [MetricsModule].
	Metrics MetricsOptions `koanf:"metrics"`
	// Observer, if set, receives job lifecycle signals for metrics (insert
	// counters + completion/duration). Code-supplied (e.g. fx.Decorate), not
	// from config.
	Observer Observer `koanf:"-"`
	// Clock supplies "now" for retry scheduling (nil = wall clock). The fx
	// [Module] injects clock.Clock from the graph when present.
	Clock clock.Clock `koanf:"-"`
}

// QueueOptions groups queue-wide settings.
type QueueOptions struct {
	Default QueueDefault `koanf:"default"`
	// Queues registers named queues beyond "default" (e.g. critical/high/low/
	// bulk). A job inserted into a queue not listed here (or "default") is
	// never worked. Config: jobs.queue.queues.<name>.max.
	Queues map[string]QueueConfig `koanf:"queues"`
}

// QueueDefault groups settings for river's "default" queue.
type QueueDefault struct {
	Max int `koanf:"max"`
}

// QueueConfig configures one named queue.
type QueueConfig struct {
	Max int `koanf:"max"`
}

// JobOptions groups per-job defaults.
type JobOptions struct {
	// Timeout caps each job invocation (default 30s). Set to -1 to disable the
	// global cap and defer to each worker's Timeout() — needed for long-running
	// workers (scans, transcodes) that legitimately exceed 30s.
	Timeout     time.Duration `koanf:"timeout"`
	MaxAttempts int           `koanf:"max_attempts"`
}

// DefaultOptions returns the opinionated defaults (enabled, default queue
// with 10 workers, 30s job timeout, 25 max attempts).
func DefaultOptions() Options {
	return Options{
		Enabled:          true,
		Queue:            QueueOptions{Default: QueueDefault{Max: 10}},
		Job:              JobOptions{Timeout: 30 * time.Second, MaxAttempts: 25},
		FetchCooldown:    100 * time.Millisecond,
		RescueStuckAfter: time.Hour,
		Stop:             StopOptions{Soft: defaultSoftStop, Hard: defaultHardStop},
		Tracing:          TracingOptions{Propagate: true},
		Metrics:          DefaultMetricsOptions(),
	}
}

// Client aliases river.Client[pgx.Tx] so apps don't have to import
// riverpgxv5 just to spell out the generic.
type Client = river.Client[pgx.Tx]

// Workers aliases river.Workers.
type Workers = river.Workers

// NewWorkers returns a fresh worker registry. Apps usually inject the
// *Workers provided by [Module] instead and register via [Register].
func NewWorkers() *Workers { return river.NewWorkers() }

// Register adds a typed worker to the registry without exposing River's panic
// API to application startup.
func Register[T river.JobArgs](w *Workers, worker river.Worker[T]) error {
	if w == nil {
		return errors.New("jobs: register: nil workers")
	}
	if reflect.ValueOf(*w).IsZero() {
		return errors.New("jobs: register: uninitialized workers; use jobs.NewWorkers")
	}
	if validate.IsNil(worker) {
		return errors.New("jobs: register: nil worker")
	}
	if err := river.AddWorkerSafely(w, worker); err != nil {
		return fmt.Errorf("jobs: register: %w", err)
	}
	return nil
}

func (o Options) withDefaults() Options {
	d := DefaultOptions()
	if o.Queue.Default.Max == 0 {
		o.Queue.Default.Max = d.Queue.Default.Max
	}
	if o.Job.Timeout == 0 {
		o.Job.Timeout = d.Job.Timeout
	}
	if o.Job.MaxAttempts == 0 {
		o.Job.MaxAttempts = d.Job.MaxAttempts
	}
	if o.FetchCooldown == 0 {
		o.FetchCooldown = d.FetchCooldown
	}
	if o.RescueStuckAfter == 0 {
		o.RescueStuckAfter = d.RescueStuckAfter
	}
	if validate.IsNil(o.Observer) {
		o.Observer = nil
	}
	if validate.IsNil(o.Clock) {
		o.Clock = nil
	}
	o.Stop = o.Stop.withDefaults()
	o.Retry = o.Retry.withDefaults()
	return o
}

func (o Options) validate() error {
	if err := o.Stop.validate(); err != nil {
		return err
	}
	if err := o.Retry.validate(); err != nil {
		return err
	}
	for name, qc := range o.Queue.Queues {
		if qc.Max < 0 {
			return fmt.Errorf("jobs: queue %q: max workers must not be negative", name)
		}
	}
	return nil
}

func hasObserver(observer Observer) bool { return !validate.IsNil(observer) }

// New constructs a river client. When workers is nil (no queues
// registered) the client is insert-only — useful for producer-only
// services that enqueue jobs for another service to work.
func New(pool *pgxpool.Pool, opts Options, workers *Workers, logger *slog.Logger) (*Client, error) {
	return NewClient(riverpgxv5.New(pool), opts, workers, logger)
}

// NewClient builds a River client over any River driver with the same
// Options handling as [New]; driver packages such as jobs/sqlite build on it.
func NewClient[TTx any](
	driver riverdriver.Driver[TTx],
	opts Options,
	workers *Workers,
	logger *slog.Logger,
) (*river.Client[TTx], error) {
	if logger == nil {
		return nil, errors.New("jobs: nil logger")
	}
	if validate.IsNil(driver) {
		return nil, errors.New("jobs: nil driver")
	}
	cfg, err := riverConfig(opts, workers, logger)
	if err != nil {
		return nil, err
	}
	c, err := river.NewClient(driver, cfg)
	if err != nil {
		return nil, fmt.Errorf("jobs: new client: %w", err)
	}
	return c, nil
}

func riverConfig(opts Options, workers *Workers, logger *slog.Logger) (*river.Config, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	opts = opts.withDefaults()
	cfg := &river.Config{
		Logger:                      logger,
		JobTimeout:                  opts.Job.Timeout,
		MaxAttempts:                 opts.Job.MaxAttempts,
		FetchCooldown:               opts.FetchCooldown,
		FetchPollInterval:           opts.FetchPollInterval,
		RescueStuckJobsAfter:        opts.RescueStuckAfter,
		CompletedJobRetentionPeriod: opts.CompletedJobRetention,
		DiscardedJobRetentionPeriod: opts.DiscardedJobRetention,
		Plugins:                     tracingPlugins(opts.Tracing),
	}
	if hasObserver(opts.Observer) {
		cfg.Middleware = []rivertype.Middleware{&insertObserver{obs: opts.Observer}}
	}
	if opts.Retry.enabled() {
		policy, err := NewRetryPolicy(opts.Retry, opts.Clock)
		if err != nil {
			return nil, err
		}
		cfg.RetryPolicy = policy
	}
	if workers != nil {
		cfg.Queues = riverQueues(opts.Queue)
		cfg.Workers = workers
	}
	return cfg, nil
}

func riverQueues(q QueueOptions) map[string]river.QueueConfig {
	queues := make(map[string]river.QueueConfig, len(q.Queues)+1)
	queues[river.QueueDefault] = river.QueueConfig{MaxWorkers: q.Default.Max}
	for name, qc := range q.Queues {
		queues[name] = river.QueueConfig{MaxWorkers: max(qc.Max, 1)}
	}
	return queues
}

func loadOptions(cfg *config.Config) (Options, error) { return LoadOptions(cfg) }

// LoadOptions reads the jobs.* config keys over [DefaultOptions]; driver
// modules such as jobs/sqlite share it.
func LoadOptions(cfg *config.Config) (Options, error) {
	if cfg == nil {
		return Options{}, errors.New("jobs: load options: nil config")
	}
	opts := DefaultOptions()
	if err := cfg.Unmarshal("jobs", &opts); err != nil {
		return Options{}, fmt.Errorf("jobs: load options: %w", err)
	}
	return opts, nil
}

// Module wires a *Client + *Workers into fx. The client starts when
// workers are registered (Queues != nil) and stops gracefully on fx Stop.
// Requires *pgxpool.Pool (db/pgx) and *slog.Logger (log/) in the graph.
//
// Apps register workers by injecting *Workers via fx.Invoke:
//
//	fx.Invoke(func(w *jobs.Workers) error {
//	    return jobs.Register(w, &MyWorker{})
//	})
var Module = fx.Module(
	"golusoris.jobs",
	fx.Provide(loadOptions),
	fx.Provide(NewWorkers),
	fx.Provide(provideClientFx),
	fx.Provide(ProvideInserter[pgx.Tx]),
)

// clientParams lets fx inject an optional clock.Clock for retry scheduling.
type clientParams struct {
	fx.In
	LC      fx.Lifecycle
	Pool    *pgxpool.Pool
	Opts    Options
	Workers *Workers
	Logger  *slog.Logger
	Clock   clock.Clock `optional:"true"`
}

func provideClientFx(p clientParams) (*Client, error) {
	opts := p.Opts
	if validate.IsNil(opts.Clock) {
		opts.Clock = p.Clock
	}
	return provideClient(p.LC, p.Pool, opts, p.Workers, p.Logger)
}

func provideClient(
	lc fx.Lifecycle,
	pool *pgxpool.Pool,
	opts Options,
	workers *Workers,
	logger *slog.Logger,
) (*Client, error) {
	opts = opts.withDefaults()
	if !opts.Enabled {
		return nil, nil //nolint:nilnil // documented disabled contract
	}
	clientWorkers := workers
	if opts.ProducerOnly {
		clientWorkers = nil
	}
	c, err := New(pool, opts, clientWorkers, logger)
	if err != nil {
		return nil, err
	}
	if opts.ProducerOnly {
		return c, nil
	}
	AppendLifecycle(lc, c, opts, logger)
	return c, nil
}

// AppendLifecycle starts c on fx Start (subscribing opts.Observer) and drains
// it with [Drain] on fx Stop, bounded by opts.Stop and the fx stop context.
func AppendLifecycle[TTx any](lc fx.Lifecycle, c *river.Client[TTx], opts Options, logger *slog.Logger) {
	opts = opts.withDefaults()
	var obsCancel func()
	// River derives its fetch and work contexts from the Start context and fx
	// ends that one once start completes, so River runs under a lifecycle-owned
	// context until OnStop; the drain on OnStop bounds shutdown.
	runCtx, runCancel := context.WithCancel(context.Background())
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if err := startDetached(ctx, runCtx, c); err != nil {
				runCancel()
				return err
			}
			if hasObserver(opts.Observer) {
				obsCancel = ObserveClient(c, opts.Observer)
			}
			return nil
		},
		OnStop: func(ctx context.Context) error {
			defer runCancel()
			if obsCancel != nil {
				obsCancel()
			}
			if err := Drain(ctx, c, opts.Stop, logger); err != nil {
				return fmt.Errorf("jobs: stop: %w", err)
			}
			return nil
		},
	})
}

// startDetached starts c under runCtx so River outlives the fx start context,
// while startup itself stays bounded by startCtx.
func startDetached[TTx any](startCtx, runCtx context.Context, c *river.Client[TTx]) error {
	errCh := make(chan error, 1)
	go func() { errCh <- c.Start(runCtx) }()
	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("jobs: start: %w", err)
		}
		return nil
	case <-startCtx.Done():
		return fmt.Errorf("jobs: start: %w", startCtx.Err())
	}
}
