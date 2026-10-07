// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package timescale

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/config"
)

// Options configures [Module] under the "db.timescale" config key.
type Options struct {
	// Require is the minimum edition start accepts: "" (no check),
	// "apache" (extension installed), or "community" (TSL features).
	Require Edition `koanf:"require"`
	// DetectTimeout bounds the start-time capability probe.
	DetectTimeout time.Duration `koanf:"detect_timeout"`
}

// DefaultOptions returns the defaults: no edition requirement, 5s probe.
func DefaultOptions() Options {
	return Options{DetectTimeout: 5 * time.Second}
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := DefaultOptions()
	if err := cfg.Unmarshal("db.timescale", &opts); err != nil {
		return Options{}, fmt.Errorf("db/timescale: load options: %w", err)
	}
	if err := opts.validate(); err != nil {
		return Options{}, err
	}
	return opts, nil
}

func (o Options) validate() error {
	if o.DetectTimeout <= 0 {
		return errors.New("db/timescale: detect timeout must be positive")
	}
	switch o.Require {
	case "", EditionNone, EditionApache, EditionCommunity:
		return nil
	case EditionUnknown:
		return errors.New("db/timescale: required edition must be apache or community, not unknown")
	default:
		return fmt.Errorf("db/timescale: unknown required edition %q", o.Require)
	}
}

// check reports whether caps satisfies the required edition. An unknown
// edition satisfies "apache" (the extension is present) but never "community".
func (o Options) check(caps Capabilities) error {
	if o.Require == "" || o.Require == EditionNone {
		return nil
	}
	if !caps.Installed() {
		return fmt.Errorf("db/timescale: require %s: %w", o.Require, ErrExtensionMissing)
	}
	if o.Require == EditionCommunity && caps.Edition != EditionCommunity {
		return fmt.Errorf("db/timescale: require community, detected %s: %w", caps.Edition, ErrUnsupportedEdition)
	}
	return nil
}

// Module provides a [*DB] over the [*pgxpool.Pool] from db/pgx, logs the
// detected edition on start, and fails start when db.timescale.require is
// not met.
var Module = fx.Module(
	"golusoris.db.timescale",
	fx.Provide(loadOptions),
	fx.Provide(New),
	fx.Invoke(registerProbe),
)

type probeParams struct {
	fx.In

	Lifecycle fx.Lifecycle
	DB        *DB
	Options   Options
	Logger    *slog.Logger
}

func registerProbe(p probeParams) {
	p.Lifecycle.Append(fx.Hook{OnStart: func(ctx context.Context) error {
		probeCtx, cancel := context.WithTimeout(ctx, p.Options.DetectTimeout)
		defer cancel()
		caps, err := p.DB.Capabilities(probeCtx)
		if err != nil {
			return err
		}
		p.Logger.InfoContext(probeCtx, "db/timescale: detected",
			slog.String("edition", string(caps.Edition)),
			slog.String("version", caps.Version),
		)
		return p.Options.check(caps)
	}})
}
