// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package twotier

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/rueidis"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/cache/memory"
	"github.com/golusoris/golusoris/cache/singleflight"
	"github.com/golusoris/golusoris/core/config"
	"github.com/golusoris/golusoris/core/validate"
	"github.com/golusoris/golusoris/realtime/pubsub"
)

// L2 backends selectable by cache.twotier.l2.
const (
	// L2Redis uses a rueidis.Client as the distributed tier (default).
	L2Redis = "redis"
	// L2None builds an L1-only cache for single-process deployments.
	L2None = "none"
)

// defaultBroadcastTimeout bounds one invalidation publish.
const defaultBroadcastTimeout = 2 * time.Second

// Options tunes the two-tier cache.
//
// Config key prefix: cache.twotier.*
//
//	cache.twotier.l1_ttl = 1m   # L1 (in-process) TTL, 0 = inherit otter default
//	cache.twotier.l2_ttl = 5m   # L2 (redis) TTL, 0 = no expiry
//	cache.twotier.l2 = redis    # redis | none (L1-only)
//	cache.twotier.invalidation.enabled = false  # cross-replica L1 eviction
//	cache.twotier.invalidation.topic = golusoris.cache.twotier.invalidate
//	cache.twotier.invalidation.timeout = 2s     # bound per broadcast
type Options struct {
	// L1TTL is the time-to-live for entries written into L1 by this cache.
	// L1 entries also obey cache/memory's configured TTL. Default 1m.
	L1TTL time.Duration `koanf:"l1_ttl"`
	// L2TTL is the time-to-live for entries written into L2 (Redis).
	// 0 means no expiry. Default 5m.
	L2TTL time.Duration `koanf:"l2_ttl"`
	// L2 selects the distributed tier: [L2Redis] (default) or [L2None].
	L2 string `koanf:"l2"`
	// Invalidation configures cross-replica L1 eviction.
	Invalidation InvalidationOptions `koanf:"invalidation"`
}

// InvalidationOptions configures cross-replica L1 eviction.
type InvalidationOptions struct {
	// Enabled broadcasts every mutation and evicts on peer notices. It needs
	// a [Broadcaster] and a positive L1TTL, which bounds staleness when a
	// notice is lost.
	Enabled bool `koanf:"enabled"`
	// Topic is the pub/sub topic (default [DefaultInvalidationTopic]).
	Topic string `koanf:"topic"`
	// Timeout bounds one broadcast (default 2s).
	Timeout time.Duration `koanf:"timeout"`
}

func defaultOptions() Options {
	return Options{
		L1TTL:        time.Minute,
		L2TTL:        5 * time.Minute,
		L2:           L2Redis,
		Invalidation: InvalidationOptions{Topic: DefaultInvalidationTopic, Timeout: defaultBroadcastTimeout},
	}
}

func loadOptions(cfg *config.Config) (Options, error) {
	opts := defaultOptions()
	if err := cfg.Unmarshal("cache.twotier", &opts); err != nil {
		return Options{}, fmt.Errorf("cache/twotier: load options: %w", err)
	}
	return opts, nil
}

// redisL2 adapts a rueidis.Client to the package-internal [l2] interface.
type redisL2 struct {
	client rueidis.Client
}

// noneL2 is the absent distributed tier of an L1-only cache.
type noneL2 struct{}

func (noneL2) Get(context.Context, string) ([]byte, bool, error)        { return nil, false, nil }
func (noneL2) Set(context.Context, string, []byte, time.Duration) error { return nil }
func (noneL2) Del(context.Context, string) error                        { return nil }

// DelPrefix refuses an empty prefix exactly like the Redis tier.
func (noneL2) DelPrefix(_ context.Context, prefix string) error {
	if prefix == "" {
		return errors.New("cache/twotier: refusing to scan-delete an empty prefix")
	}
	return nil
}

func (r redisL2) Get(ctx context.Context, key string) ([]byte, bool, error) {
	raw, err := r.client.Do(ctx, r.client.B().Get().Key(key).Build()).AsBytes()
	if err != nil {
		if rueidis.IsRedisNil(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("cache/twotier: redis get: %w", err)
	}
	return raw, true, nil
}

func (r redisL2) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	b := r.client.B().Set().Key(key).Value(rueidis.BinaryString(val))
	var cmd rueidis.Completed
	if ttl > 0 {
		cmd = b.Px(ttl).Build()
	} else {
		cmd = b.Build()
	}
	if err := r.client.Do(ctx, cmd).Error(); err != nil {
		return fmt.Errorf("cache/twotier: redis set: %w", err)
	}
	return nil
}

func (r redisL2) Del(ctx context.Context, key string) error {
	if err := r.client.Do(ctx, r.client.B().Del().Key(key).Build()).Error(); err != nil {
		return fmt.Errorf("cache/twotier: redis del: %w", err)
	}
	return nil
}

// scanCount bounds work per SCAN round-trip (a hint, not a hard page size).
const scanCount = 256

// maxScanRounds bounds the cursor-paged SCAN loop in DelPrefix (HISS-02): a
// well-behaved Redis server always returns cursor 0 within a handful of
// rounds relative to keyspace size, so this is a generous ceiling that never
// trips in practice; it exists to fail closed instead of looping forever
// against a server that keeps cycling non-zero cursors.
const maxScanRounds = 1_000_000

// scanRoundFunc performs one paginated round starting at cursor and returns
// the next cursor to resume from (0 means the scan is complete).
type scanRoundFunc func(cursor uint64) (nextCursor uint64, err error)

// runBoundedScan drives fn from cursor 0 until it reports completion (a
// returned cursor of 0) or maxRounds is exceeded, whichever comes first
// (HISS-02: scalar loop bound). Extracted from DelPrefix so the bound itself
// is unit-testable without a Redis server.
func runBoundedScan(maxRounds int, fn scanRoundFunc) error {
	cursor := uint64(0)
	for range maxRounds {
		next, err := fn(cursor)
		if err != nil {
			return err
		}
		if next == 0 {
			return nil
		}
		cursor = next
	}
	return fmt.Errorf("cache/twotier: exceeded %d scan rounds without completing", maxRounds)
}

// DelPrefix removes every key matching "<prefix>*" via cursor-paged SCAN, then
// UNLINKs the matched keys per page (UNLINK reclaims memory off the main
// thread, unlike DEL). An empty prefix is rejected to avoid scanning the whole
// keyspace, which is never what a typed view wants.
func (r redisL2) DelPrefix(ctx context.Context, prefix string) error {
	if prefix == "" {
		return errors.New("cache/twotier: refusing to scan-delete an empty prefix")
	}
	match := prefix + "*"
	return runBoundedScan(maxScanRounds, func(cursor uint64) (uint64, error) {
		entry, err := r.client.Do(
			ctx,
			r.client.B().Scan().Cursor(cursor).Match(match).Count(scanCount).Build(),
		).AsScanEntry()
		if err != nil {
			return 0, fmt.Errorf("cache/twotier: redis scan %q: %w", match, err)
		}
		if len(entry.Elements) > 0 {
			if derr := r.client.Do(
				ctx,
				r.client.B().Unlink().Key(entry.Elements...).Build(),
			).Error(); derr != nil {
				return 0, fmt.Errorf("cache/twotier: redis unlink: %w", derr)
			}
		}
		return entry.Cursor, nil
	})
}

var errInvalidDependency = errors.New("cache/twotier: invalid dependency")

// Option supplies an optional dependency to [New].
type Option func(*dependencies)

type dependencies struct {
	redis       rueidis.Client
	broadcaster Broadcaster
}

// WithRedis supplies the L2 client that Options.L2 = [L2Redis] needs.
func WithRedis(client rueidis.Client) Option {
	return func(d *dependencies) { d.redis = client }
}

// WithBroadcaster supplies the channel that Options.Invalidation.Enabled needs.
func WithBroadcaster(b Broadcaster) Option {
	return func(d *dependencies) { d.broadcaster = b }
}

// New builds a [TwoTier] over l1. Options.L2 = [L2Redis] needs [WithRedis];
// [L2None] builds an L1-only cache and rejects a Redis client.
// Options.Invalidation.Enabled needs [WithBroadcaster]; call
// [TwoTier.Listen] to receive peer notices.
func New(l1 *memory.Cache, opts Options, logger *slog.Logger, options ...Option) (*TwoTier, error) {
	if err := validateBase(opts, l1, logger); err != nil {
		return nil, err
	}
	var deps dependencies
	for _, option := range options {
		if option != nil {
			option(&deps)
		}
	}
	tier, err := resolveL2(opts.L2, deps.redis)
	if err != nil {
		return nil, err
	}
	broadcaster, timeout, err := resolveInvalidation(opts, deps.broadcaster)
	if err != nil {
		return nil, err
	}
	tt := &TwoTier{
		l1:               l1,
		l2:               tier,
		logger:           logger,
		l1TTL:            opts.L1TTL,
		l2TTL:            opts.L2TTL,
		group:            singleflight.New[flightKey, []byte](),
		origin:           rand.Text(),
		broadcaster:      broadcaster,
		broadcastTimeout: timeout,
	}
	logger.Debug(
		"cache/twotier: started",
		slog.Duration("l1_ttl", opts.L1TTL),
		slog.Duration("l2_ttl", opts.L2TTL),
		slog.String("l2", opts.L2),
		slog.Bool("invalidation", broadcaster != nil),
	)
	return tt, nil
}

func validateBase(opts Options, l1 *memory.Cache, logger *slog.Logger) error {
	if opts.L1TTL < 0 || opts.L2TTL < 0 {
		return errors.New("cache/twotier: TTLs must not be negative")
	}
	if logger == nil {
		return fmt.Errorf("%w: logger", errInvalidDependency)
	}
	if l1 == nil {
		return fmt.Errorf("%w: L1 cache", errInvalidDependency)
	}
	return nil
}

func resolveL2(mode string, client rueidis.Client) (l2, error) {
	switch mode {
	case L2Redis, "":
		if validate.IsNil(client) {
			return nil, fmt.Errorf("%w: Redis client", errInvalidDependency)
		}
		return redisL2{client: client}, nil
	case L2None:
		if !validate.IsNil(client) {
			return nil, errors.New("cache/twotier: l2 = none takes no Redis client")
		}
		return noneL2{}, nil
	default:
		return nil, fmt.Errorf("cache/twotier: unknown l2 %q (want %s or %s)", mode, L2Redis, L2None)
	}
}

func resolveInvalidation(opts Options, broadcaster Broadcaster) (Broadcaster, time.Duration, error) {
	inv := opts.Invalidation
	if validate.IsNil(broadcaster) {
		broadcaster = nil
	}
	if !inv.Enabled {
		if broadcaster != nil {
			return nil, 0, errors.New("cache/twotier: broadcaster supplied but invalidation.enabled is false")
		}
		return nil, 0, nil
	}
	if broadcaster == nil {
		return nil, 0, fmt.Errorf("%w: invalidation broadcaster", errInvalidDependency)
	}
	if opts.L1TTL <= 0 {
		return nil, 0, errors.New("cache/twotier: invalidation needs a positive l1_ttl to bound staleness of lost notices")
	}
	if inv.Timeout < 0 {
		return nil, 0, errors.New("cache/twotier: invalidation timeout must not be negative")
	}
	if inv.Timeout == 0 {
		return broadcaster, defaultBroadcastTimeout, nil
	}
	return broadcaster, inv.Timeout, nil
}

// moduleParams carries the optional backends; only the configured ones are required.
type moduleParams struct {
	fx.In

	Options Options
	L1      *memory.Cache
	Logger  *slog.Logger
	Redis   rueidis.Client `optional:"true"`
	Bus     pubsub.Bus     `optional:"true"`
}

// newModuleTwoTier wires a [TwoTier] from config and the fx graph.
func newModuleTwoTier(p moduleParams) (*TwoTier, error) {
	var options []Option
	if p.Options.L2 != L2None {
		options = append(options, WithRedis(p.Redis))
	}
	if p.Options.Invalidation.Enabled {
		broadcaster, err := NewBusBroadcaster(p.Bus, p.Options.Invalidation.Topic, p.Logger)
		if err != nil {
			return nil, fmt.Errorf("cache/twotier: invalidation needs a pubsub.Bus (realtime/pubsub/redis): %w", err)
		}
		options = append(options, WithBroadcaster(broadcaster))
	}
	return New(p.L1, p.Options, p.Logger, options...)
}

// registerListener subscribes to peer invalidations for the app's lifetime.
func registerListener(lc fx.Lifecycle, tt *TwoTier) {
	stop := func() {}
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			stop = tt.Listen()
			return nil
		},
		OnStop: func(context.Context) error {
			stop()
			return nil
		},
	})
}

// Module provides *twotier.TwoTier to the fx graph. It requires *memory.Cache
// (golusoris.CacheMemory) plus config + log from golusoris.Core, a
// rueidis.Client (golusoris.CacheRedis) unless cache.twotier.l2 = none, and a
// pubsub.Bus (realtime/pubsub/redis) when cache.twotier.invalidation.enabled.
var Module = fx.Module(
	"golusoris.cache.twotier",
	fx.Provide(loadOptions),
	fx.Provide(newModuleTwoTier),
	fx.Invoke(registerListener),
)
