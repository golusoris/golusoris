// Package config is golusoris's config layer: a thin koanf v2 wrapper that
// loads from env + optional YAML/JSON files, with file-watch enabled by
// default so a mounted k8s ConfigMap update triggers reload callbacks
// without a pod restart.
//
// SIGHUP also triggers a re-read.
//
// Apps add structured config by calling [Config.Unmarshal] into their own
// struct.
package config

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/knadh/koanf/parsers/json"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
	"go.uber.org/fx"
)

// Options configures the loader. Zero value is usable: env-only, prefix
// "APP_", delimiter ".".
type Options struct {
	// EnvPrefix filters env vars (e.g. "APP_" matches APP_DB_HOST). Stripped
	// before being keyed into koanf.
	EnvPrefix string
	// Delimiter is the koanf path separator. Default ".".
	Delimiter string
	// Files is a list of file paths to load (YAML or JSON, by extension).
	// Missing files are skipped silently. The first existing file wins on
	// overlapping keys; later files override earlier ones.
	Files []string
	// Watch enables file-watch on Files. Default true. Disable in tests.
	Watch bool
}

// Config is the dependency apps inject.
type Config struct {
	k    *koanf.Koanf
	opts Options

	mu        sync.RWMutex
	listeners []func()
}

// Get returns the value for path or "" if absent.
func (c *Config) Get(path string) string { return c.k.String(path) }

// Bool / Int / String / Strings / etc. — pass-through to koanf for the most
// common types so callers don't import koanf directly for one lookup.
func (c *Config) Bool(path string) bool        { return c.k.Bool(path) }
func (c *Config) Int(path string) int          { return c.k.Int(path) }
func (c *Config) Int64(path string) int64      { return c.k.Int64(path) }
func (c *Config) Float(path string) float64    { return c.k.Float64(path) }
func (c *Config) String(path string) string    { return c.k.String(path) }
func (c *Config) Strings(p string) []string    { return c.k.Strings(p) }
func (c *Config) Exists(path string) bool      { return c.k.Exists(path) }
func (c *Config) All() map[string]any          { return c.k.All() }

// Unmarshal decodes a key (or "" for root) into a struct.
func (c *Config) Unmarshal(path string, into any) error {
	if err := c.k.Unmarshal(path, into); err != nil {
		return fmt.Errorf("config: unmarshal %q: %w", path, err)
	}
	return nil
}

// OnChange registers a callback fired whenever a watched config file changes
// or SIGHUP is received. Callbacks run synchronously in the watcher goroutine
// — keep them quick or fan out to a worker.
func (c *Config) OnChange(fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.listeners = append(c.listeners, fn)
}

func (c *Config) fire() {
	c.mu.RLock()
	ls := append([]func(){}, c.listeners...)
	c.mu.RUnlock()
	for _, fn := range ls {
		fn()
	}
}

// New loads the configuration with the given options.
func New(opts Options) (*Config, error) {
	if opts.Delimiter == "" {
		opts.Delimiter = "."
	}

	k := koanf.New(opts.Delimiter)

	// Files first (later override earlier).
	for _, path := range opts.Files {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		var p koanf.Parser
		switch strings.ToLower(filepath.Ext(path)) {
		case ".yaml", ".yml":
			p = yaml.Parser()
		case ".json":
			p = json.Parser()
		default:
			return nil, fmt.Errorf("config: unsupported file extension %q", path)
		}
		if err := k.Load(file.Provider(path), p); err != nil {
			return nil, fmt.Errorf("config: load %s: %w", path, err)
		}
	}

	// Env on top: APP_DB_HOST -> db.host
	envProvider := env.Provider(opts.EnvPrefix, opts.Delimiter, func(s string) string {
		return strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(s, opts.EnvPrefix)), "_", opts.Delimiter)
	})
	if err := k.Load(envProvider, nil); err != nil {
		return nil, fmt.Errorf("config: load env: %w", err)
	}

	c := &Config{k: k, opts: opts}
	return c, nil
}

// startWatch wires file watchers + SIGHUP handler. Returns a stop func.
func (c *Config) startWatch(ctx context.Context) func() {
	stops := make([]func(), 0, len(c.opts.Files)+1)

	if c.opts.Watch {
		for _, path := range c.opts.Files {
			if _, err := os.Stat(path); err != nil {
				continue
			}
			fp := file.Provider(path)
			path := path
			_ = fp.Watch(func(_ any, err error) {
				if err != nil {
					return
				}
				_ = c.k.Load(fp, parserFor(path))
				c.fire()
			})
			stops = append(stops, func() { _ = fp.Unwatch() })
		}
	}

	// SIGHUP -> reload all files + fire listeners.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP)
	hupCtx, cancel := context.WithCancel(ctx)
	go func() {
		for {
			select {
			case <-hupCtx.Done():
				return
			case <-sigs:
				for _, path := range c.opts.Files {
					if _, err := os.Stat(path); err != nil {
						continue
					}
					_ = c.k.Load(file.Provider(path), parserFor(path))
				}
				c.fire()
			}
		}
	}()
	stops = append(stops, func() { signal.Stop(sigs); cancel() })

	return func() {
		for _, s := range stops {
			s()
		}
	}
}

func parserFor(path string) koanf.Parser {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		return yaml.Parser()
	case ".json":
		return json.Parser()
	}
	return nil
}

// Module provides a [*Config] built from default Options (env-prefix "APP_",
// no files). Apps can override by supplying their own [Options] before this
// module via fx.Replace or fx.Decorate.
var Module = fx.Module("golusoris.config",
	fx.Provide(func() Options {
		return Options{
			EnvPrefix: "APP_",
			Delimiter: ".",
			Watch:     true,
		}
	}),
	fx.Provide(func(opts Options, lc fx.Lifecycle) (*Config, error) {
		c, err := New(opts)
		if err != nil {
			return nil, err
		}
		var stop func()
		lc.Append(fx.Hook{
			OnStart: func(ctx context.Context) error {
				stop = c.startWatch(ctx)
				return nil
			},
			OnStop: func(_ context.Context) error {
				if stop != nil {
					stop()
				}
				return nil
			},
		})
		return c, nil
	}),
)
