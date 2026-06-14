// Package twotier composes the L1 in-process cache (cache/memory, otter) and
// the L2 distributed cache (cache/redis, rueidis) into a single read-through,
// write-through cache with singleflight de-duplication.
//
// A read goes L1 → L2 → loader: a hit in a faster tier short-circuits and
// back-fills the tiers it skipped. A write fans out to both tiers. Concurrent
// loads of the same key are coalesced so the loader runs once.
//
// Values cross the L1/L2 boundary as JSON: L1 stores the live Go value, L2
// stores its JSON encoding (Redis is a byte store, and JSON keeps the cache
// language-agnostic across replicas).
//
// Disabled / nil-passthrough mode: a nil *Cache is a valid no-op cache. Every
// method is nil-safe — Get falls straight through to the loader, Set/Delete do
// nothing — so call sites never branch on whether caching is configured.
//
// Usage:
//
//	fx.New(
//	    golusoris.Core,
//	    golusoris.CacheMemory, // *memory.Cache (L1)
//	    golusoris.CacheRedis,  // rueidis.Client (L2)
//	    golusoris.CacheTwoTier // *twotier.TwoTier
//	)
//
//	// Get a typed view and read through it:
//	users := twotier.Typed[*User](tt, "user")
//	u, err := users.Get(ctx, id, func(ctx context.Context) (*User, error) {
//	    return db.LoadUser(ctx, id)
//	})
//
// Config key prefix: cache.twotier.*
package twotier

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/golusoris/golusoris/cache/memory"
	"github.com/golusoris/golusoris/cache/singleflight"
)

// l2 is the minimal distributed-tier contract twotier needs. The rueidis
// adapter ([redisL2]) implements it in production; tests supply an in-memory
// stub. Keeping the surface this small is what makes the package hermetically
// testable without the full rueidis.Client interface.
type l2 interface {
	// Get returns the raw bytes for key and true, (nil, false) on miss.
	Get(ctx context.Context, key string) ([]byte, bool, error)
	// Set stores raw bytes for key with the given TTL (0 = no expiry).
	Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
	// Del removes key.
	Del(ctx context.Context, key string) error
}

// Loader fetches a value from the origin on a full cache miss.
type Loader[V any] func(ctx context.Context) (V, error)

// TwoTier is the untyped two-tier cache. It is created by the fx module and
// shared across the app; callers obtain a type-safe view via [Typed].
//
// A nil *TwoTier is a valid disabled cache (see package doc).
type TwoTier struct {
	l1     *memory.Cache
	l2     l2
	logger *slog.Logger
	l1TTL  time.Duration
	l2TTL  time.Duration
	group  *singleflight.Group[string, []byte]
}

// Typed is a type-safe view over a [TwoTier] with a key prefix. Multiple typed
// views can share one TwoTier without colliding. V is the stored value type.
//
// A view built from a nil *TwoTier is itself a disabled no-op view.
type Typed[V any] struct {
	tt     *TwoTier
	prefix string
}

// NewTyped returns a typed view over tt with the given key prefix. tt may be
// nil, in which case the view is a no-op (Get always calls the loader).
func NewTyped[V any](tt *TwoTier, prefix string) *Typed[V] {
	return &Typed[V]{tt: tt, prefix: prefix}
}

func (t *Typed[V]) key(k string) string {
	return t.prefix + ":" + k
}

// Get returns the value for k, reading through L1 → L2 → loader. The first
// tier that has it wins and back-fills the faster tiers it skipped. Concurrent
// Gets for the same key share a single loader invocation.
//
// On a disabled (nil) cache, Get calls loader directly and caches nothing.
func (t *Typed[V]) Get(ctx context.Context, k string, loader Loader[V]) (V, error) {
	if t.tt == nil {
		return loader(ctx)
	}
	key := t.key(k)

	if v, ok := t.l1Get(key); ok {
		return v, nil
	}

	raw, _, err := t.tt.group.Do(ctx, key, func(ctx context.Context) ([]byte, error) {
		return t.load(ctx, key, loader)
	})
	if err != nil {
		var zero V
		return zero, fmt.Errorf("cache/twotier: get %q: %w", key, err)
	}

	var v V
	if uerr := json.Unmarshal(raw, &v); uerr != nil {
		var zero V
		return zero, fmt.Errorf("cache/twotier: decode loaded value: %w", uerr)
	}
	t.tt.l1.Set(key, v)
	return v, nil
}

// l1Get reads the typed value from L1, guarding the any→V assertion.
func (t *Typed[V]) l1Get(key string) (V, bool) {
	raw, ok := t.tt.l1.GetIfPresent(key)
	if !ok {
		var zero V
		return zero, false
	}
	v, ok := raw.(V)
	return v, ok
}

// load runs inside singleflight: it checks L2, then falls back to the loader,
// populating L2 on an origin hit. It returns the JSON bytes so L1 can be
// back-filled by the caller after the assertion succeeds.
func (t *Typed[V]) load(ctx context.Context, key string, loader Loader[V]) ([]byte, error) {
	if raw, ok, err := t.tt.l2.Get(ctx, key); err != nil {
		t.tt.logger.WarnContext(ctx, "cache/twotier: L2 get failed, falling through", slog.Any("error", err))
	} else if ok {
		return raw, nil
	}

	v, err := loader(ctx)
	if err != nil {
		return nil, fmt.Errorf("loader: %w", err)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("cache/twotier: encode loaded value: %w", err)
	}
	if serr := t.tt.l2.Set(ctx, key, raw, t.tt.l2TTL); serr != nil {
		t.tt.logger.WarnContext(ctx, "cache/twotier: L2 set failed", slog.Any("error", serr))
	}
	return raw, nil
}

// Set writes v to both tiers (write-through). On a disabled cache it is a
// no-op.
func (t *Typed[V]) Set(ctx context.Context, k string, v V) error {
	if t.tt == nil {
		return nil
	}
	key := t.key(k)
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("cache/twotier: encode value: %w", err)
	}
	t.tt.l1.Set(key, v)
	if serr := t.tt.l2.Set(ctx, key, raw, t.tt.l2TTL); serr != nil {
		return fmt.Errorf("cache/twotier: L2 set: %w", serr)
	}
	t.tt.group.Forget(key)
	return nil
}

// Delete removes k from both tiers. On a disabled cache it is a no-op.
func (t *Typed[V]) Delete(ctx context.Context, k string) error {
	if t.tt == nil {
		return nil
	}
	key := t.key(k)
	t.tt.l1.Invalidate(key)
	t.tt.group.Forget(key)
	if derr := t.tt.l2.Del(ctx, key); derr != nil {
		return fmt.Errorf("cache/twotier: L2 delete: %w", derr)
	}
	return nil
}
