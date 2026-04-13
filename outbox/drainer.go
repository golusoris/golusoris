package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/clock"
	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/jobs"
)

// DrainerOptions tunes the drainer loop.
type DrainerOptions struct {
	Enabled  bool          `koanf:"enabled"`
	Interval time.Duration `koanf:"interval"`
	Batch    int           `koanf:"batch"`
}

// DefaultDrainerOptions returns: disabled, 1s poll interval, 100-event batch.
func DefaultDrainerOptions() DrainerOptions {
	return DrainerOptions{Interval: time.Second, Batch: 100}
}

// Dispatcher converts a pending outbox Event into a river Insert call.
// Apps supply one: the common shape is a type-switch on Event.Kind that
// unmarshals Payload into the right JobArgs and returns it.
//
// Returning a nil JobArgs + nil error drops the event (marks dispatched
// without enqueuing) — useful for events whose downstream no longer
// cares.
type Dispatcher func(ctx context.Context, ev Event) (river.JobArgs, *river.InsertOpts, error)

// Drainer polls the outbox + dispatches pending events to river. Runs
// under a leader so only one replica drains; [Module] handles that
// wiring.
type Drainer struct {
	pool       *pgxpool.Pool
	client     *jobs.Client
	dispatcher Dispatcher
	logger     *slog.Logger
	clk        clock.Clock
	opts       DrainerOptions
}

// NewDrainer builds a Drainer. Apps usually don't call this directly —
// use [Module].
func NewDrainer(pool *pgxpool.Pool, client *jobs.Client, dispatcher Dispatcher, logger *slog.Logger, clk clock.Clock, opts DrainerOptions) *Drainer {
	if opts.Interval == 0 {
		opts.Interval = time.Second
	}
	if opts.Batch == 0 {
		opts.Batch = 100
	}
	return &Drainer{
		pool: pool, client: client, dispatcher: dispatcher,
		logger: logger, clk: clk, opts: opts,
	}
}

// Run blocks until ctx is canceled, polling + dispatching. Drains once
// immediately on entry so enqueued events clear fast without waiting a
// full interval.
func (d *Drainer) Run(ctx context.Context) error {
	d.logger.Info("outbox/drainer: starting",
		slog.Duration("interval", d.opts.Interval),
		slog.Int("batch", d.opts.Batch),
	)
	for {
		if err := d.drain(ctx); err != nil {
			d.logger.Warn("outbox/drainer: drain failed", slog.String("error", err.Error()))
			// Continue — transient errors shouldn't kill the drainer.
		}
		select {
		case <-ctx.Done():
			return nil
		case <-d.clk.After(d.opts.Interval):
		}
	}
}

func (d *Drainer) drain(ctx context.Context) error {
	events, err := Pending(ctx, d.pool, d.opts.Batch)
	if err != nil {
		return err
	}
	for _, ev := range events {
		if dispatchErr := d.dispatchOne(ctx, ev); dispatchErr != nil {
			d.logger.Warn("outbox/drainer: dispatch failed",
				slog.Int64("id", ev.ID),
				slog.String("kind", ev.Kind),
				slog.String("error", dispatchErr.Error()),
			)
			_ = MarkFailed(ctx, d.pool, ev.ID, dispatchErr)
			continue
		}
		if markErr := MarkDispatched(ctx, d.pool, ev.ID); markErr != nil {
			d.logger.Warn("outbox/drainer: mark dispatched",
				slog.Int64("id", ev.ID),
				slog.String("error", markErr.Error()),
			)
		}
	}
	return nil
}

func (d *Drainer) dispatchOne(ctx context.Context, ev Event) error {
	args, insertOpts, dispatchErr := d.dispatcher(ctx, ev)
	if dispatchErr != nil {
		return fmt.Errorf("outbox: dispatcher %q: %w", ev.Kind, dispatchErr)
	}
	if args == nil {
		// Caller dropped the event (returned nil, nil, nil).
		return nil
	}
	if _, err := d.client.Insert(ctx, args, insertOpts); err != nil {
		return fmt.Errorf("outbox: insert river job: %w", err)
	}
	return nil
}

// Unmarshal decodes ev.Payload into out. Convenience for dispatcher
// implementations.
func Unmarshal(ev Event, out any) error {
	if err := json.Unmarshal(ev.Payload, out); err != nil {
		return fmt.Errorf("outbox: unmarshal %q payload: %w", ev.Kind, err)
	}
	return nil
}

func loadDrainerOptions(cfg *config.Config) (DrainerOptions, error) {
	opts := DefaultDrainerOptions()
	if err := cfg.Unmarshal("outbox", &opts); err != nil {
		return DrainerOptions{}, fmt.Errorf("outbox: load options: %w", err)
	}
	return opts, nil
}

// Module wires a leader-gated drainer into fx. The caller supplies a
// Dispatcher via fx.Supply or fx.Provide.
//
// Apps wrap this with a leader Module so only one replica drains:
//
//	fx.New(
//	    golusoris.Core, golusoris.DB, golusoris.Jobs,
//	    fx.Supply(myDispatcher),
//	    outbox.Module,
//	    leaderpg.Module(leader.Callbacks{
//	        OnStartedLeading: func(ctx context.Context) {
//	            // Drainer's Run starts automatically when fx does; the
//	            // leader callback is where apps plug in leader-only work
//	            // beyond draining. outbox.Module itself is leader-gated
//	            // via the StartIf option wrapper in its Invoke.
//	        },
//	    }),
//	)
//
// Simpler + more explicit: apps call outbox.NewDrainer manually inside
// their leader callback and plumb their own lifecycle.
var Module = fx.Module("golusoris.outbox",
	fx.Provide(loadDrainerOptions),
	fx.Provide(NewDrainer),
	fx.Invoke(func(lc fx.Lifecycle, d *Drainer, opts DrainerOptions) {
		if !opts.Enabled {
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		lc.Append(fx.Hook{
			OnStart: func(_ context.Context) error {
				go func() {
					defer close(done)
					_ = d.Run(ctx)
				}()
				return nil
			},
			OnStop: func(_ context.Context) error {
				cancel()
				<-done
				return nil
			},
		})
	}),
)
