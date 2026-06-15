package fleet

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/riverqueue/river"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/ai/tiny"
	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/jobs"
)

// Options tunes the fleet recipe.
type Options struct {
	// Enabled is the master switch (default true).
	Enabled bool `koanf:"enabled"`
	// QueuePrefix is prepended to each capability to form the river queue
	// name (default [DefaultQueuePrefix]).
	QueuePrefix string `koanf:"queue_prefix"`
	// Capabilities is this node's served capability set. A node fetches a
	// job only when the job's capability is in this set (default ["cpu"]).
	Capabilities []string `koanf:"capabilities"`
	// MaxWorkers caps concurrent predictions per capability queue
	// (default 4). Below 1 is clamped to 1.
	MaxWorkers int `koanf:"max_workers"`
	// PredictTimeout caps one Predict (Load+Predict) call (default 60s).
	PredictTimeout time.Duration `koanf:"predict_timeout"`
	// MaxInputBytes bounds the re-encoded job input (default 1 MiB).
	MaxInputBytes int `koanf:"max_input_bytes"`
}

// DefaultOptions returns the opinionated defaults (enabled, "tiny"
// prefix, cpu-only, 4 workers, 60s timeout, 1 MiB input cap).
func DefaultOptions() Options {
	return Options{
		Enabled:        true,
		QueuePrefix:    DefaultQueuePrefix,
		Capabilities:   []string{"cpu"},
		MaxWorkers:     4,
		PredictTimeout: 60 * time.Second,
		MaxInputBytes:  DefaultMaxInputBytes,
	}
}

func (o Options) withDefaults() Options {
	d := DefaultOptions()
	if o.QueuePrefix == "" {
		o.QueuePrefix = d.QueuePrefix
	}
	if len(o.Capabilities) == 0 {
		o.Capabilities = d.Capabilities
	}
	if o.MaxWorkers < 1 {
		o.MaxWorkers = d.MaxWorkers
	}
	if o.PredictTimeout == 0 {
		o.PredictTimeout = d.PredictTimeout
	}
	if o.MaxInputBytes <= 0 {
		o.MaxInputBytes = d.MaxInputBytes
	}
	return o
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := DefaultOptions()
	if err := cfg.Unmarshal("tiny.fleet", &opts); err != nil {
		return Options{}, fmt.Errorf("ai/tiny/serve/fleet: load options: %w", err)
	}
	return opts, nil
}

// capabilities converts the string config slice to typed Capabilities.
func (o Options) capabilities() []Capability {
	out := make([]Capability, 0, len(o.Capabilities))
	for _, c := range o.Capabilities {
		out = append(out, Capability(c))
	}
	return out
}

// newFleet builds the controller from the river client + registry.
func newFleet(opts Options, registry tiny.Registry, client *jobs.Client) (*Fleet, error) {
	opts = opts.withDefaults()
	if client == nil {
		return nil, errors.New("ai/tiny/serve/fleet: nil jobs.Client (is jobs.Module wired and enabled?)")
	}
	return NewFleet(registry, client, opts.QueuePrefix)
}

// registerWorker wires the node-side Worker onto the shared jobs.Workers
// registry. Must run before the river client starts (fx orders Provide
// before the jobs client's OnStart hook, which is what registers
// workers with river).
func registerWorker(opts Options, registry tiny.Registry, factory PredictorFactory, sink ResultSink, workers *jobs.Workers, logger *slog.Logger) error {
	opts = opts.withDefaults()
	w, err := NewWorker(registry, factory, sink, opts.capabilities(),
		opts.PredictTimeout, opts.MaxInputBytes, logger)
	if err != nil {
		return err
	}
	jobs.Register(workers, w)
	return nil
}

// addQueues registers this node's capability queues on the running river
// client so its producers fetch capability-matched jobs. river's
// QueueBundle.Add starts a producer per queue on an already-started
// client.
func addQueues(lc fx.Lifecycle, opts Options, client *jobs.Client, logger *slog.Logger) error {
	opts = opts.withDefaults()
	caps, err := normalizeCaps(opts.capabilities())
	if err != nil {
		return err
	}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			for _, c := range caps {
				name := queueName(opts.QueuePrefix, c)
				if addErr := client.Queues().Add(name, river.QueueConfig{MaxWorkers: opts.MaxWorkers}); addErr != nil {
					return fmt.Errorf("ai/tiny/serve/fleet: add queue %q: %w", name, addErr)
				}
				logger.DebugContext(ctx, "ai/tiny/serve/fleet: queue subscribed",
					slog.String("queue", name), slog.Int("max_workers", opts.MaxWorkers))
			}
			return nil
		},
	})
	return nil
}

// Module wires the distributed-inference fleet recipe.
//
// Provides:
//   - *Fleet — the controller handle apps use to Submit predictions.
//
// Requires in the graph:
//   - tiny.Registry — model lookup (provide your own, or tiny.MemoryRegistry).
//   - *jobs.Client + *jobs.Workers — from jobs.Module (river).
//   - PredictorFactory — builds a tiny.Predictor per job
//     (fleet.SingletonFactory(ollamaPredictor) for the common case).
//   - ResultSink — persists predictions (fleet.ResultSinkFunc(...)).
//   - *slog.Logger — from log.Module.
//
// The node side registers a Worker on the capability queues named
// "<prefix>.<capability>" and adds those queues to the running river
// client so this replica fetches only capability-matched jobs.
//
// Usage:
//
//	fx.New(
//	    golusoris.Core,
//	    golusoris.DB,
//	    jobs.Module,
//	    fx.Supply(fx.Annotate(myRegistry, fx.As(new(tiny.Registry)))),
//	    fx.Provide(func(p *ollama.Predictor) fleet.PredictorFactory {
//	        return fleet.SingletonFactory(p)
//	    }),
//	    fx.Provide(func() fleet.ResultSink { return mySink }),
//	    fleet.Module,
//	)
var Module = fx.Module(
	"golusoris.tiny.fleet",
	fx.Provide(loadOptions),
	fx.Provide(newFleet),
	fx.Invoke(func(opts Options, registry tiny.Registry, factory PredictorFactory, sink ResultSink, workers *jobs.Workers, logger *slog.Logger) error {
		if !opts.Enabled {
			return nil
		}
		return registerWorker(opts, registry, factory, sink, workers, logger)
	}),
	fx.Invoke(func(lc fx.Lifecycle, opts Options, client *jobs.Client, logger *slog.Logger) error {
		if !opts.Enabled {
			return nil
		}
		return addQueues(lc, opts, client, logger)
	}),
)
