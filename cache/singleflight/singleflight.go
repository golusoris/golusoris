// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package singleflight exposes [golang.org/x/sync/singleflight] as a
// tiny typed wrapper and fx module. It de-duplicates concurrent
// identical reads so only one goroutine hits the backing store while
// the others wait and share the result.
//
// Typical use:
//
//	// Provide via fx (or construct directly):
//	g := singleflight.New[string, *User]()
//
//	// In a handler / service:
//	user, err := g.Do(ctx, userID, func(ctx context.Context) (*User, error) {
//	    return db.LoadUser(ctx, userID)
//	})
package singleflight

import (
	"context"
	"sync"

	"golang.org/x/sync/singleflight"
)

const sharedFlightKey = "flight"

type keyedFlight struct {
	group singleflight.Group
	users int
}

// Group de-duplicates concurrent calls with the same key K.
// V is the result type. Errors are propagated to every waiter.
type Group[K comparable, V any] struct {
	mu      sync.Mutex
	flights map[K]*keyedFlight
}

// New returns an initialised Group.
func New[K comparable, V any]() *Group[K, V] {
	return &Group[K, V]{flights: make(map[K]*keyedFlight)}
}

// Do executes fn exactly once for concurrent callers sharing the same
// key. fn receives the context of the first caller — other callers'
// contexts are not forwarded (singleflight design). Callers that need
// per-call context cancellation should check ctx.Done() after Do
// returns.
func (g *Group[K, V]) Do(ctx context.Context, key K, fn func(ctx context.Context) (V, error)) (V, bool, error) {
	flight := g.acquire(key)
	defer g.release(key, flight)
	v, err, shared := flight.group.Do(sharedFlightKey, func() (any, error) {
		return fn(ctx)
	})
	if err != nil {
		var zero V
		return zero, shared, err //nolint:wrapcheck // propagate as-is
	}
	return v.(V), shared, nil //nolint:forcetypeassert // fn guarantees V
}

// Forget evicts the in-flight or cached result for key, so the next
// caller will execute fn again.
func (g *Group[K, V]) Forget(key K) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if flight := g.flights[key]; flight != nil {
		flight.group.Forget(sharedFlightKey)
	}
}

func (g *Group[K, V]) acquire(key K) *keyedFlight {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.flights == nil {
		g.flights = make(map[K]*keyedFlight)
	}
	flight := g.flights[key]
	if flight == nil {
		flight = &keyedFlight{}
		g.flights[key] = flight
	}
	flight.users++
	return flight
}

func (g *Group[K, V]) release(key K, flight *keyedFlight) {
	g.mu.Lock()
	defer g.mu.Unlock()
	flight.users--
	if flight.users == 0 && g.flights[key] == flight {
		delete(g.flights, key)
	}
}
