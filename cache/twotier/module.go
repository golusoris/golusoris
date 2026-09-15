// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package twotier

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/rueidis"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/cache/memory"
	"github.com/golusoris/golusoris/cache/singleflight"
	"github.com/golusoris/golusoris/core/config"
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
	logger.Debug(
		"cache/twotier: started",
		slog.Duration("l1_ttl", opts.L1TTL),
		slog.Duration("l2_ttl", opts.L2TTL),
	)
	return tt
}

// Module provides *twotier.TwoTier to the fx graph. It requires *memory.Cache
// (golusoris.CacheMemory) and rueidis.Client (golusoris.CacheRedis) plus
// config + log from golusoris.Core.
var Module = fx.Module(
	"golusoris.cache.twotier",
	fx.Provide(loadOptions),
	fx.Provide(newTwoTier),
)
