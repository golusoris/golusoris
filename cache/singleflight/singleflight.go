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
	"fmt"

	"golang.org/x/sync/singleflight"
)

// Group de-duplicates concurrent calls with the same key K.
// V is the result type. Errors are propagated to every waiter.
type Group[K comparable, V any] struct {
	g singleflight.Group
}

// New returns an initialised Group.
func New[K comparable, V any]() *Group[K, V] {
	return &Group[K, V]{}
}

// Do executes fn exactly once for concurrent callers sharing the same
// key. fn receives the context of the first caller — other callers'
// contexts are not forwarded (singleflight design). Callers that need
// per-call context cancellation should check ctx.Done() after Do
// returns.
func (g *Group[K, V]) Do(ctx context.Context, key K, fn func(ctx context.Context) (V, error)) (V, bool, error) {
	k := keyString(key)
	v, err, shared := g.g.Do(k, func() (any, error) {
		return fn(ctx) //nolint:wrapcheck // caller's error; don't re-wrap
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
	g.g.Forget(keyString(key))
}

func keyString[K comparable](k K) string {
	return fmt.Sprintf("%v", k)
}
