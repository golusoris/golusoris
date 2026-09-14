// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package plugin provides a type-safe, in-process extension-point registry.
//
// It lets framework modules define named extension points and lets apps (or
// other modules) register implementations without import cycles. This is the
// pure-Go alternative to .so plugins: no CGO, no subprocess, works on every
// GOOS.
//
// # Extension points
//
// An extension point is a named slot that accepts implementations of a
// specific interface. Define one per concern:
//
//	// In your module:
//	var PaymentProviders = plugin.New[PaymentProvider]("payment.providers")
//
//	// In an app or feature module (e.g. from an fx.Invoke):
//	if err := PaymentProviders.Register("stripe", &StripeProvider{}); err != nil { return err }
//
//	// At runtime:
//	p, ok := PaymentProviders.Get("stripe")
//	// Or, failing fast at startup:
//	p, err := PaymentProviders.Lookup("stripe")
//
// # fx integration
//
// [Registry.Module] returns an fx.Module that provides the registry itself
// and exposes fx.Annotate group helpers so implementations can be wired via
// dependency injection instead of manual Register calls.
package plugin

import (
	"errors"
	"fmt"
	"sync"
)

// ErrDuplicate is returned by [Registry.Register] when the key is already taken.
var ErrDuplicate = errors.New("plugin: duplicate registration")

// ErrNotRegistered is returned by [Registry.Lookup] when no impl is registered.
var ErrNotRegistered = errors.New("plugin: no implementation registered")

// Registry is a thread-safe map from name → T.
// T is typically an interface type.
type Registry[T any] struct {
	name  string
	mu    sync.RWMutex
	items map[string]T
}

// New creates a named Registry. The name is used only in error messages.
func New[T any](name string) *Registry[T] {
	return &Registry[T]{name: name, items: make(map[string]T)}
}

// Register adds impl under the given key. Returns [ErrDuplicate] on a repeat
// registration so misconfiguration surfaces at startup (fx.Invoke), not runtime.
func (r *Registry[T]) Register(key string, impl T) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.items[key]; dup {
		return fmt.Errorf("%w: %s: key %q", ErrDuplicate, r.name, key)
	}
	r.items[key] = impl
	return nil
}

// MustRegister is like [Register] but replaces an existing entry instead of
// failing. Useful in tests that need to swap implementations.
func (r *Registry[T]) MustRegister(key string, impl T) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items[key] = impl
}

// Get returns the implementation registered under key, or (zero, false).
func (r *Registry[T]) Get(key string) (T, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.items[key]
	return v, ok
}

// Lookup returns the implementation or [ErrNotRegistered] with a descriptive
// message. Use at startup (fx.Invoke) to fail fast on misconfiguration.
func (r *Registry[T]) Lookup(key string) (T, error) {
	v, ok := r.Get(key)
	if !ok {
		return v, fmt.Errorf("%w: %s: key %q", ErrNotRegistered, r.name, key)
	}
	return v, nil
}

// Keys returns a snapshot of all registered keys in undefined order.
func (r *Registry[T]) Keys() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.items))
	for k := range r.items {
		out = append(out, k)
	}
	return out
}

// All returns a snapshot of all registered (key, impl) pairs.
func (r *Registry[T]) All() map[string]T {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]T, len(r.items))
	for k, v := range r.items {
		out[k] = v
	}
	return out
}

// Len returns the number of registered implementations.
func (r *Registry[T]) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.items)
}

// Entry is a key-value pair returned by [Registry.Entries].
type Entry[T any] struct {
	Key  string
	Impl T
}

// Entries returns all registrations as a slice. Order is undefined.
func (r *Registry[T]) Entries() []Entry[T] {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Entry[T], 0, len(r.items))
	for k, v := range r.items {
		out = append(out, Entry[T]{Key: k, Impl: v})
	}
	return out
}
