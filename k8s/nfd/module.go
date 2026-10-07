// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package nfd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"time"

	"go.uber.org/fx"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/config"
)

// Source returns the labels to publish. It runs on start and on every
// refresh, bounded by [Options.Timeout].
type Source func(ctx context.Context) (map[string]string, error)

// Static returns a Source that always yields a copy of labels.
func Static(labels map[string]string) Source {
	fixed := maps.Clone(labels)
	return func(context.Context) (map[string]string, error) { return maps.Clone(fixed), nil }
}

// Options configures [Module]. Config keys (env APP_K8S_NFD_*):
//
//	k8s.nfd.enabled  # master switch (default false)
//	k8s.nfd.dir      # features.d directory (default DefaultDir)
//	k8s.nfd.name     # feature file name (required when enabled)
//	k8s.nfd.ttl      # expiry written into the file (default 10m)
//	k8s.nfd.refresh  # rewrite period, must be below ttl (default 2m)
//	k8s.nfd.timeout  # bound on one Source call + write (default 10s)
type Options struct {
	Enabled bool          `koanf:"enabled"`
	Dir     string        `koanf:"dir"`
	Name    string        `koanf:"name"`
	TTL     time.Duration `koanf:"ttl"`
	Refresh time.Duration `koanf:"refresh"`
	Timeout time.Duration `koanf:"timeout"`
}

// DefaultOptions returns disabled options with NFD's default directory and
// a 10m expiry refreshed every 2m.
func DefaultOptions() Options {
	return Options{
		Dir:     DefaultDir,
		TTL:     10 * time.Minute,
		Refresh: 2 * time.Minute,
		Timeout: 10 * time.Second,
	}
}

func (o Options) validate() error {
	if err := validateName(o.Name); err != nil {
		return fmt.Errorf("nfd: k8s.nfd.name: %w", err)
	}
	switch {
	case o.Dir == "":
		return errors.New("nfd: k8s.nfd.dir is empty")
	case o.Refresh <= 0, o.Timeout <= 0:
		return fmt.Errorf("nfd: refresh %v and timeout %v must be positive", o.Refresh, o.Timeout)
	case o.TTL <= o.Refresh:
		return fmt.Errorf("nfd: ttl %v must exceed refresh %v or labels lapse between rewrites", o.TTL, o.Refresh)
	}
	return nil
}

// Writer publishes one feature file from a [Source].
type Writer struct {
	opts Options
	src  Source
	clk  clock.Clock
}

// NewWriter validates opts and returns a Writer.
func NewWriter(opts Options, src Source, clk clock.Clock) (*Writer, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	if src == nil || clk == nil {
		return nil, errors.New("nfd: source and clock are required")
	}
	return &Writer{opts: opts, src: src, clk: clk}, nil
}

// Refresh reads the source and rewrites the file with expiry now+TTL.
func (w *Writer) Refresh(ctx context.Context) error {
	labels, err := w.src(ctx)
	if err != nil {
		return fmt.Errorf("nfd: source: %w", err)
	}
	return WriteFeatureFileUntil(w.opts.Dir, w.opts.Name, labels, w.clk.Now().Add(w.opts.TTL))
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := DefaultOptions()
	if err := cfg.Unmarshal("k8s.nfd", &opts); err != nil {
		return Options{}, fmt.Errorf("nfd: load options: %w", err)
	}
	return opts, nil
}

// Module writes the feature file from src on start (failing start on error)
// and rewrites it every k8s.nfd.refresh until stop. The file stays after
// stop; its expiry retires the labels if no successor rewrites it.
func Module(src Source) fx.Option {
	return fx.Module(
		"golusoris.k8s.nfd",
		fx.Provide(loadOptions),
		fx.Invoke(func(lc fx.Lifecycle, opts Options, clk clock.Clock, logger *slog.Logger) error {
			if !opts.Enabled {
				return nil
			}
			w, err := NewWriter(opts, src, clk)
			if err != nil {
				return err
			}
			register(lc, w, logger)
			return nil
		}),
	)
}

func register(lc fx.Lifecycle, w *Writer, logger *slog.Logger) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	lc.Append(fx.Hook{
		OnStart: func(startCtx context.Context) error {
			callCtx, callCancel := context.WithTimeout(startCtx, w.opts.Timeout)
			defer callCancel()
			if err := w.Refresh(callCtx); err != nil {
				cancel()
				close(done)
				return err
			}
			go func() {
				defer close(done)
				for ctx.Err() == nil {
					select {
					case <-ctx.Done():
						return
					case <-w.clk.After(w.opts.Refresh):
					}
					loopCtx, loopCancel := context.WithTimeout(ctx, w.opts.Timeout)
					if err := w.Refresh(loopCtx); err != nil {
						logger.WarnContext(loopCtx, "nfd: refresh failed", slog.String("error", err.Error()))
					}
					loopCancel()
				}
			}()
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			cancel()
			select {
			case <-done:
				return nil
			case <-stopCtx.Done():
				return fmt.Errorf("nfd: refresh loop did not stop before the stop deadline: %w", stopCtx.Err())
			}
		},
	})
}
