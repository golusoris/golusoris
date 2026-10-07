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

	"github.com/golusoris/golusoris/core/validate"
)

const (
	defaultSoftStop = 10 * time.Second
	defaultHardStop = 5 * time.Second
)

// StopOptions bounds the two-phase drain fx Stop runs. Soft + Hard defaults
// (10s + 5s) fit fx's 15s default stop timeout.
type StopOptions struct {
	// Soft is how long Stop waits for running jobs after fetching stops.
	Soft time.Duration `koanf:"soft"`
	// Hard is how long StopAndCancel waits for cancelled jobs to return.
	Hard time.Duration `koanf:"hard"`
}

// Stopper is the shutdown surface of a River client; *Client satisfies it.
type Stopper interface {
	Stop(ctx context.Context) error
	StopAndCancel(ctx context.Context) error
}

func (o StopOptions) withDefaults() StopOptions {
	if o.Soft == 0 {
		o.Soft = defaultSoftStop
	}
	if o.Hard == 0 {
		o.Hard = defaultHardStop
	}
	return o
}

func (o StopOptions) validate() error {
	if o.Soft < 0 || o.Hard < 0 {
		return errors.New("jobs: stop: soft and hard timeouts must not be negative")
	}
	return nil
}

// Drain stops c in two phases: a soft Stop bounded by opts.Soft that lets
// running jobs finish, then — only if the soft phase did not complete — a
// StopAndCancel bounded by opts.Hard that cancels job contexts. Both phases
// are also bounded by ctx. Zero durations take the defaults.
func Drain(ctx context.Context, c Stopper, opts StopOptions, logger *slog.Logger) error {
	if validate.IsNil(ctx) {
		return errors.New("jobs: drain: nil context")
	}
	if validate.IsNil(c) {
		return errors.New("jobs: drain: nil client")
	}
	if logger == nil {
		return errors.New("jobs: drain: nil logger")
	}
	if err := opts.validate(); err != nil {
		return err
	}
	opts = opts.withDefaults()

	softCtx, softCancel := context.WithTimeout(ctx, opts.Soft)
	softErr := c.Stop(softCtx)
	softCancel()
	if softErr == nil {
		return nil
	}
	logger.WarnContext(ctx, "jobs: soft stop incomplete; cancelling running jobs",
		slog.Duration("soft", opts.Soft), slog.String("error", softErr.Error()))

	// StopAndCancel cancels job contexts before it checks hardCtx, so running
	// jobs see cancellation even when the fx stop deadline has already passed.
	hardCtx, hardCancel := context.WithTimeout(ctx, opts.Hard)
	defer hardCancel()
	if err := c.StopAndCancel(hardCtx); err != nil {
		return fmt.Errorf("jobs: drain: hard stop: %w", errors.Join(softErr, err))
	}
	return nil
}
