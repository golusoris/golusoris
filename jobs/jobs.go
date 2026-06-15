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
//	jobs.queue.default.max    # max concurrent workers on the default queue (default 10)
//	jobs.job.timeout          # per-job timeout (default 30s; workers can override)
//	jobs.job.max_attempts     # default max attempts (default 25 = ~3 days retries)
//	jobs.fetch_cooldown       # pg LISTEN cooldown (default 100ms)
//	jobs.rescue_stuck_after   # rescue jobs stuck running for this long (default 1h)
//
// See [river]'s docs for full Config reference.
package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
)

// Options tunes the river client.
type Options struct {
	Enabled       bool          `koanf:"enabled"`
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
	// Observer, if set, receives job lifecycle signals for metrics (insert
	// counters + completion/duration). Code-supplied (e.g. fx.Decorate), not
	// from config.
	Observer Observer `koanf:"-"`
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

// Register adds a typed worker to the registry. Thin sugar over
// river.AddWorker so apps don't import river directly.
func Register[T river.JobArgs](w *Workers, worker river.Worker[T]) {
	river.AddWorker(w, worker)
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
	return o
}

// New constructs a river client. When workers is nil (no queues
// registered) the client is insert-only — useful for producer-only
// services that enqueue jobs for another service to work.
func New(pool *pgxpool.Pool, opts Options, workers *Workers, logger *slog.Logger) (*Client, error) {
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
	}
	if opts.Observer != nil {
		cfg.Middleware = []rivertype.Middleware{&insertObserver{obs: opts.Observer}}
	}
	if workers != nil {
		queues := map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: opts.Queue.Default.Max},
		}
		for name, qc := range opts.Queue.Queues {
			maxWorkers := qc.Max
			if maxWorkers < 1 {
				maxWorkers = 1
			}
			queues[name] = river.QueueConfig{MaxWorkers: maxWorkers}
		}
		cfg.Queues = queues
		cfg.Workers = workers
	}
	c, err := river.NewClient(riverpgxv5.New(pool), cfg)
	if err != nil {
		return nil, fmt.Errorf("jobs: new client: %w", err)
	}
	return c, nil
}

func loadOptions(cfg *config.Config) (Options, error) {
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
//	fx.Invoke(func(w *jobs.Workers) {
//	    jobs.Register(w, &MyWorker{})
//	})
var Module = fx.Module(
	"golusoris.jobs",
	fx.Provide(loadOptions),
	fx.Provide(NewWorkers),
	fx.Provide(func(lc fx.Lifecycle, pool *pgxpool.Pool, opts Options, workers *Workers, logger *slog.Logger) (*Client, error) {
		if !opts.Enabled {
			return nil, nil //nolint:nilnil // documented disabled contract
		}
		c, err := New(pool, opts, workers, logger)
		if err != nil {
			return nil, err
		}
		var obsCancel func()
		lc.Append(fx.Hook{
			OnStart: func(ctx context.Context) error {
				if err := c.Start(ctx); err != nil {
					return fmt.Errorf("jobs: start: %w", err)
				}
				if opts.Observer != nil {
					obsCancel = Observe(c, opts.Observer)
				}
				return nil
			},
			OnStop: func(ctx context.Context) error {
				if obsCancel != nil {
					obsCancel()
				}
				if err := c.Stop(ctx); err != nil {
					return fmt.Errorf("jobs: stop: %w", err)
				}
				return nil
			},
		})
		return c, nil
	}),
)
