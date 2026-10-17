// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Ported from SnatchArr api/internal/leaderx at 6111de5a.

package gate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/leader"
	"github.com/golusoris/golusoris/leader/internal/hook"
	leaderpg "github.com/golusoris/golusoris/leader/pg"
)

// LoopsGroup is the fx value group name that periodic loops register in.
const LoopsGroup = "leader_loops"

// Options tunes leader gate options, matching leader/pg.
type Options = leaderpg.Options

// DefaultOptions returns default options for the leader gate.
func DefaultOptions() Options {
	return leaderpg.DefaultOptions()
}

// loadOptions reads options from the "leader" config key via koanf.
func loadOptions(cfg *config.Config) (Options, error) {
	opts := DefaultOptions()
	if err := cfg.Unmarshal("leader", &opts); err != nil {
		return Options{}, fmt.Errorf("leader/gate: load options: %w", err)
	}
	return opts, nil
}

// Params collects registered loops and dependencies for the leader gate.
type Params struct {
	fx.In

	Loops  []Loop `group:"leader_loops"`
	Clock  clock.Clock
	Logger *slog.Logger
	Opts   Options
	Pool   *pgxpool.Pool `optional:"true"`
	Elect  Elect         `optional:"true"`
}

func newGate(p Params) *Gate { return New(p.Clock, p.Logger, p.Loops...) }

func electorFor(p Params) Elect {
	if p.Elect != nil {
		return p.Elect
	}
	if !p.Opts.Enabled {
		return nil
	}
	p.Logger.Info("leader/gate: election enabled",
		slog.String("name", p.Opts.Name), slog.String("identity", p.Opts.Identity))
	return func(ctx context.Context, cb leader.Callbacks) error {
		if p.Pool == nil {
			return errors.New("leader/gate: pool is required when leader election is enabled")
		}
		if err := leaderpg.Run(ctx, p.Pool, p.Opts, p.Clock, cb); err != nil {
			return fmt.Errorf("leader/gate: %w", err)
		}
		return nil
	}
}

// run ties the gate to the fx lifecycle: it runs for the whole process and stops with it.
func run(lc fx.Lifecycle, g *Gate, p Params) {
	elect := electorFor(p)
	retry := p.Opts.PG.Retry
	if retry <= 0 {
		retry = DefaultRetry
	}
	hook.RunUntilStop(lc, p.Logger, "leader/gate", func(ctx context.Context) error {
		return g.Run(ctx, elect, retry)
	})
}

// Module provides the gate and runs it for the process lifetime. It runs its own
// election, so an application uses it instead of leader/pg.Module, never both.
var Module = fx.Module("golusoris.leader.gate",
	fx.Provide(loadOptions, newGate),
	fx.Invoke(run),
)

// Provide registers an already-provided *T as a leader-gated loop:
//
//	fx.Provide(NewTicker, gate.Provide[*Ticker]())
func Provide[T Loop]() any {
	return fx.Annotate(asLoop[T], fx.ResultTags(`group:"`+LoopsGroup+`"`))
}

func asLoop[T Loop](l T) Loop { return l }
