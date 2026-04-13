// Package memory provides a typed, in-process L1 cache backed by
// [maypok86/otter/v2]. Exposed as an fx module so apps opt in with a
// single line.
//
// Usage:
//
//	fx.New(
//	    golusoris.Core,
//	    memory.Module, // provides *memory.Cache
//	)
//
// Apps that need a type-safe view over the underlying cache use [Typed]:
//
//	fx.Invoke(func(c *memory.Cache) {
//	    sessions := memory.Typed[string, Session](c, "session")
//	    sessions.Set("abc", sess)
//	    s, ok := sessions.Get("abc")
//	})
//
// Config key prefix: cache.memory.*
package memory

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/maypok86/otter/v2"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
)

// Cache is the concrete otter cache type used by this module.
// Key is string; value is any so a single pool serves all callers.
// Use [Typed] to get a type-safe accessor.
type Cache = otter.Cache[string, any]

// Options tunes the cache.
type Options struct {
	// MaxSize is the soft maximum number of entries (default 10_000).
	MaxSize int `koanf:"max_size"`
	// TTL is the default time-to-live for entries (default 5m, 0 = no TTL).
	TTL time.Duration `koanf:"ttl"`
}

func defaultOptions() Options {
	return Options{MaxSize: 10_000, TTL: 5 * time.Minute}
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := defaultOptions()
	if err := cfg.Unmarshal("cache.memory", &opts); err != nil {
		return Options{}, fmt.Errorf("cache/memory: load options: %w", err)
	}
	return opts, nil
}

func newCache(opts Options, logger *slog.Logger) (*Cache, error) {
	o := &otter.Options[string, any]{
		MaximumSize: opts.MaxSize,
	}
	if opts.TTL > 0 {
		o.ExpiryCalculator = otter.ExpiryWriting[string, any](opts.TTL)
	}
	c, err := otter.New(o)
	if err != nil {
		return nil, fmt.Errorf("cache/memory: build: %w", err)
	}
	logger.Debug("cache/memory: started",
		slog.Int("max_size", opts.MaxSize),
		slog.Duration("ttl", opts.TTL),
	)
	return c, nil
}

// NewForTest constructs a Cache directly without fx, for use in tests.
// ttl=0 means no expiry.
func NewForTest(maxSize int, ttl time.Duration) (*Cache, error) {
	o := &otter.Options[string, any]{MaximumSize: maxSize}
	if ttl > 0 {
		o.ExpiryCalculator = otter.ExpiryWriting[string, any](ttl)
	}
	c, err := otter.New(o)
	if err != nil {
		return nil, fmt.Errorf("cache/memory: build: %w", err)
	}
	return c, nil
}

// Module provides *memory.Cache to the fx graph.
var Module = fx.Module("golusoris.cache.memory",
	fx.Provide(loadOptions),
	fx.Provide(newCache),
	fx.Invoke(func(lc fx.Lifecycle, c *Cache) {
		lc.Append(fx.Hook{
			OnStop: func(_ context.Context) error {
				c.InvalidateAll()
				return nil
			},
		})
	}),
)

// TypedCache is a type-safe view over a *Cache with a key prefix.
// Obtain one via [Typed].
type TypedCache[K comparable, V any] struct {
	c      *Cache
	prefix string
}

// Typed returns a type-safe accessor over c. prefix is prepended to
// every key so multiple typed caches can share the underlying pool
// without colliding.
func Typed[K comparable, V any](c *Cache, prefix string) *TypedCache[K, V] {
	return &TypedCache[K, V]{c: c, prefix: prefix}
}

func (t *TypedCache[K, V]) key(k K) string {
	return fmt.Sprintf("%s:%v", t.prefix, k)
}

// Get returns the value for k and true, or zero value and false.
func (t *TypedCache[K, V]) Get(k K) (V, bool) {
	v, ok := t.c.GetIfPresent(t.key(k))
	if !ok {
		var zero V
		return zero, false
	}
	typed, ok := v.(V)
	return typed, ok
}

// Set stores k → v using the cache's configured TTL. Returns true if
// the entry was accepted (false means the cache is at capacity and
// the entry was dropped).
func (t *TypedCache[K, V]) Set(k K, v V) bool {
	_, ok := t.c.Set(t.key(k), v)
	return ok
}

// Delete removes k from the cache.
func (t *TypedCache[K, V]) Delete(k K) {
	t.c.Invalidate(t.key(k))
}
