package twotier

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/rueidis"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/cache/memory"
	"github.com/golusoris/golusoris/cache/singleflight"
	"github.com/golusoris/golusoris/config"
)

// Options tunes the two-tier cache.
//
// Config key prefix: cache.twotier.*
//
//	cache.twotier.l1_ttl = 1m   # L1 (in-process) TTL, 0 = inherit otter default
//	cache.twotier.l2_ttl = 5m   # L2 (redis) TTL, 0 = no expiry
type Options struct {
	// L1TTL is the time-to-live for entries written into L1 by this cache.
	// Note: L1 entries also obey cache/memory's own configured TTL; this knob
	// is surfaced for symmetry and future per-key expiry. Default 1m.
	L1TTL time.Duration `koanf:"l1_ttl"`
	// L2TTL is the time-to-live for entries written into L2 (Redis).
	// 0 means no expiry. Default 5m.
	L2TTL time.Duration `koanf:"l2_ttl"`
}

func defaultOptions() Options {
	return Options{L1TTL: time.Minute, L2TTL: 5 * time.Minute}
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

// newTwoTier wires a [TwoTier] from the L1 cache, the Redis client, and config.
func newTwoTier(opts Options, l1 *memory.Cache, client rueidis.Client, logger *slog.Logger) *TwoTier {
	tt := &TwoTier{
		l1:     l1,
		l2:     redisL2{client: client},
		logger: logger,
		l1TTL:  opts.L1TTL,
		l2TTL:  opts.L2TTL,
		group:  singleflight.New[string, []byte](),
	}
	logger.Debug("cache/twotier: started",
		slog.Duration("l1_ttl", opts.L1TTL),
		slog.Duration("l2_ttl", opts.L2TTL),
	)
	return tt
}

// Module provides *twotier.TwoTier to the fx graph. It requires *memory.Cache
// (golusoris.CacheMemory) and rueidis.Client (golusoris.CacheRedis) plus
// config + log from golusoris.Core.
var Module = fx.Module("golusoris.cache.twotier",
	fx.Provide(loadOptions),
	fx.Provide(newTwoTier),
)
