// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonboulle/clockwork"
	"github.com/riverqueue/river"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/core/validate"
	"github.com/golusoris/golusoris/jobs"
)

// DrainerOptions tunes the drainer loop.
type DrainerOptions struct {
	Enabled         bool          `koanf:"enabled"`
	Interval        time.Duration `koanf:"interval"`
	Batch           int           `koanf:"batch"`
	RetryDelay      time.Duration `koanf:"retry_delay"`
	DrainTimeout    time.Duration `koanf:"drain_timeout"`
	RollbackTimeout time.Duration `koanf:"rollback_timeout"`
}

// DefaultDrainerOptions returns bounded polling, batch, drain, and cleanup defaults.
func DefaultDrainerOptions() DrainerOptions {
	return DrainerOptions{
		Interval: time.Second, Batch: 100,
		RetryDelay: DefaultRetryDelay, DrainTimeout: 30 * time.Second,
		RollbackTimeout: 5 * time.Second,
	}
}

// Dispatcher converts a pending outbox Event into a river Insert call.
// Apps supply one: the common shape is a type-switch on Event.Kind that
// unmarshals Payload into the right JobArgs and returns it.
//
// Returning a nil JobArgs + nil error drops the event (marks dispatched
// without enqueuing) — useful for events whose downstream no longer
// cares.
type Dispatcher func(ctx context.Context, ev Event) (river.JobArgs, *river.InsertOpts, error)

// Drainer polls the outbox + atomically hands pending events to River.
// Concurrent drainers skip rows locked by another transaction.
type Drainer struct {
	pool       txBeginner
	client     *jobs.Client
	dispatcher Dispatcher
	logger     *slog.Logger
	clk        clock.Clock
	opts       DrainerOptions
}

type txBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

type rollbacker interface {
	Rollback(context.Context) error
}

// NewDrainer builds a Drainer. Apps usually don't call this directly —
// use [Module].
func NewDrainer(pool *pgxpool.Pool, client *jobs.Client, dispatcher Dispatcher, logger *slog.Logger, clk clock.Clock, opts DrainerOptions) *Drainer {
	opts = withDrainerDefaults(opts)
	var beginner txBeginner
	if pool != nil {
		beginner = pool
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	if validate.IsNil(clk) {
		clk = clockwork.NewRealClock()
	}
	return &Drainer{
		pool: beginner, client: client, dispatcher: dispatcher,
		logger: logger, clk: clk, opts: opts,
	}
}

func withDrainerDefaults(opts DrainerOptions) DrainerOptions {
	defaults := DefaultDrainerOptions()
	if opts.Interval <= 0 {
		opts.Interval = defaults.Interval
	}
	if opts.Batch <= 0 {
		opts.Batch = defaults.Batch
	} else if opts.Batch > MaxPendingLimit {
		opts.Batch = MaxPendingLimit
	}
	if opts.DrainTimeout <= 0 {
		opts.DrainTimeout = defaults.DrainTimeout
	}
	if opts.RollbackTimeout <= 0 {
		opts.RollbackTimeout = defaults.RollbackTimeout
	}
	opts.RetryDelay = normalizeRetryDelay(opts.RetryDelay)
	return opts
}

// Run blocks until ctx is canceled, polling + dispatching. Drains once
// immediately on entry so enqueued events clear fast without waiting a
// full interval.
func (d *Drainer) Run(ctx context.Context) error {
	d.logger.InfoContext(
		ctx, "outbox/drainer: starting",
		slog.Duration("interval", d.opts.Interval),
		slog.Int("batch", d.opts.Batch),
	)
	for ctx.Err() == nil {
		if err := d.drainOnce(ctx); err != nil {
			d.logger.WarnContext(ctx, "outbox/drainer: drain failed", slog.String("error", err.Error()))
			// Continue — transient errors shouldn't kill the drainer.
		}
		select {
		case <-ctx.Done():
			return nil
		case <-d.clk.After(d.opts.Interval):
		}
	}
	return nil
}

func (d *Drainer) drainOnce(ctx context.Context) error {
	drainCtx, cancel := context.WithTimeout(ctx, d.opts.DrainTimeout)
	defer cancel()
	return d.drain(drainCtx)
}

func (d *Drainer) drain(ctx context.Context) error {
	if validate.IsNil(d.pool) {
		return errors.New("outbox: pool is required")
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("outbox: begin drain: %w", err)
	}
	defer d.rollback(ctx, tx)

	events, err := queryPending(ctx, tx, d.opts.Batch, true)
	if err != nil {
		return err
	}
	for _, ev := range events {
		if dispatchErr := d.dispatchOne(ctx, tx, ev); dispatchErr != nil {
			d.logger.WarnContext(
				ctx, "outbox/drainer: dispatch failed",
				slog.Int64("id", ev.ID),
				slog.String("kind", ev.Kind),
				slog.String("error", dispatchErr.Error()),
			)
			if failErr := markFailed(ctx, tx, ev.ID, dispatchErr, d.opts.RetryDelay); failErr != nil {
				return failErr
			}
			continue
		}
		if markErr := markDispatched(ctx, tx, ev.ID); markErr != nil {
			return markErr
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("outbox: commit drain: %w", err)
	}
	return nil
}

func (d *Drainer) rollback(parent context.Context, tx rollbacker) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), d.opts.RollbackTimeout)
	defer cancel()
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		d.logger.ErrorContext(ctx, "outbox/drainer: rollback failed", slog.String("error", err.Error()))
	}
}

func (d *Drainer) dispatchOne(ctx context.Context, tx pgx.Tx, ev Event) error {
	if d.dispatcher == nil {
		return errors.New("outbox: dispatcher is required")
	}
	args, insertOpts, dispatchErr := d.dispatcher(ctx, ev)
	if dispatchErr != nil {
		return fmt.Errorf("outbox: dispatcher %q: %w", ev.Kind, dispatchErr)
	}
	if args == nil {
		// Caller dropped the event (returned nil, nil).
		return nil
	}
	if d.client == nil {
		return errors.New("outbox: jobs client is required")
	}
	savepoint, err := tx.Begin(ctx)
	if err != nil {
		return fmt.Errorf("outbox: begin river insert savepoint: %w", err)
	}
	if _, err = d.client.InsertTx(ctx, savepoint, args, insertOpts); err != nil {
		return rollbackSavepoint(ctx, savepoint, fmt.Errorf("outbox: insert river job: %w", err))
	}
	if err = savepoint.Commit(ctx); err != nil {
		return rollbackSavepoint(ctx, savepoint, fmt.Errorf("outbox: commit river insert savepoint: %w", err))
	}
	return nil
}

func rollbackSavepoint(ctx context.Context, savepoint pgx.Tx, cause error) error {
	if err := savepoint.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return errors.Join(cause, fmt.Errorf("outbox: rollback river insert savepoint: %w", err))
	}
	return cause
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

// Module wires a concurrency-safe drainer into fx. The caller supplies a
// Dispatcher via fx.Supply or fx.Provide. Multiple replicas may run Module;
// PostgreSQL row locks partition each batch and River insertion plus the
// dispatched marker commit atomically.
var Module = fx.Module(
	"golusoris.outbox",
	fx.Provide(loadDrainerOptions),
	fx.Provide(NewDrainer),
	fx.Invoke(func(lc fx.Lifecycle, d *Drainer, opts DrainerOptions) {
		if !opts.Enabled {
			return
		}
		var cancel context.CancelFunc
		var done chan struct{}
		lc.Append(fx.Hook{
			OnStart: func(ctx context.Context) error {
				runCtx, runCancel := context.WithCancel(context.WithoutCancel(ctx))
				cancel = runCancel
				done = make(chan struct{})
				go func() {
					defer close(done)
					if runErr := d.Run(runCtx); runErr != nil {
						d.logger.ErrorContext(runCtx, "outbox/drainer: run", slog.String("error", runErr.Error()))
					}
				}()
				return nil
			},
			OnStop: func(ctx context.Context) error {
				if cancel == nil {
					return nil
				}
				cancel()
				select {
				case <-done:
					return nil
				case <-ctx.Done():
					return fmt.Errorf("outbox: stop drainer: %w", ctx.Err())
				}
			},
		})
	}),
)
