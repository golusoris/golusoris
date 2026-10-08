// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package credentials

import (
	"context"
	"sync"
	"time"

	"github.com/golusoris/golusoris/core/clock"
)

// maxCacheEntries bounds the per-host cache; a registry fleet larger than
// this simply refetches (HISS-03: no unbounded growth).
const maxCacheEntries = 256

// DefaultCacheSkew is how long before ExpiresAt a cached credential is
// considered stale.
const DefaultCacheSkew = 5 * time.Minute

// Cache memoises credentials that carry an ExpiresAt until shortly before
// they expire, so cloud token exchanges (ECR, GAR, ACR) run once per token
// lifetime instead of once per registry request.
type Cache struct {
	next    Provider
	clock   clock.Clock
	skew    time.Duration
	mu      sync.Mutex
	entries map[string]Credential
}

// NewCache wraps next. skew <= 0 uses [DefaultCacheSkew].
func NewCache(next Provider, clk clock.Clock, skew time.Duration) *Cache {
	if skew <= 0 {
		skew = DefaultCacheSkew
	}
	return &Cache{next: next, clock: clk, skew: skew, entries: make(map[string]Credential)}
}

// Credential implements [Provider].
func (c *Cache) Credential(ctx context.Context, host string) (Credential, error) {
	if cred, ok := c.lookup(host); ok {
		return cred, nil
	}
	cred, err := c.next.Credential(ctx, host)
	if err != nil {
		return Credential{}, err //nolint:wrapcheck // transparent decorator: the inner provider already wrapped it
	}
	c.store(host, cred)
	return cred, nil
}

func (c *Cache) lookup(host string) (Credential, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cred, ok := c.entries[host]
	if !ok {
		return Credential{}, false
	}
	if !c.fresh(cred) {
		delete(c.entries, host)
		return Credential{}, false
	}
	return cred, true
}

func (c *Cache) store(host string, cred Credential) {
	if cred.ExpiresAt.IsZero() || !c.fresh(cred) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= maxCacheEntries {
		clear(c.entries)
	}
	c.entries[host] = cred
}

func (c *Cache) fresh(cred Credential) bool {
	return c.clock.Now().Add(c.skew).Before(cred.ExpiresAt)
}
