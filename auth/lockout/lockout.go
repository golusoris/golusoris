// Package lockout implements per-identity login lockout: after N failed
// attempts within a window the identity is locked for a cooldown.
//
// Storage is pluggable via [Store]. The package ships a [MemoryStore]
// for tests and single-replica deployments.
//
// Usage:
//
//	lo := lockout.New(lockout.NewMemoryStore(), lockout.Options{
//	    MaxFails: 5,
//	    Window:   15 * time.Minute,
//	    Cooldown: 30 * time.Minute,
//	})
//
//	// Before checking the password:
//	if err := lo.Check(ctx, username); err != nil { return err }
//	if !passwordOK { _ = lo.Fail(ctx, username); return ErrInvalid }
//	_ = lo.Reset(ctx, username)
package lockout

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"

	gerr "github.com/golusoris/golusoris/errors"
)

// Options tune the lockout policy.
type Options struct {
	// MaxFails is the number of failed attempts allowed before lockout.
	MaxFails int
	// Window is the duration over which fails are counted.
	Window time.Duration
	// Cooldown is how long an identity stays locked after MaxFails.
	Cooldown time.Duration
}

func (o Options) withDefaults() Options {
	if o.MaxFails == 0 {
		o.MaxFails = 5
	}
	if o.Window == 0 {
		o.Window = 15 * time.Minute
	}
	if o.Cooldown == 0 {
		o.Cooldown = 30 * time.Minute
	}
	return o
}

// State is the per-identity counter.
type State struct {
	Fails       int
	FirstFailAt time.Time
	LockedUntil time.Time
}

// Store is the backing store contract.
type Store interface {
	Get(ctx context.Context, key string) (State, error)
	Set(ctx context.Context, key string, s State, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

// Service guards an identity against brute-force login attempts.
type Service struct {
	store Store
	clk   clockwork.Clock
	opts  Options
}

// New returns a Service. clk may be nil — defaults to clockwork.NewRealClock.
func New(store Store, clk clockwork.Clock, opts Options) *Service {
	if clk == nil {
		clk = clockwork.NewRealClock()
	}
	return &Service{store: store, clk: clk, opts: opts.withDefaults()}
}

// Check returns gerr.CodeUnauthorized when key is currently locked.
func (s *Service) Check(ctx context.Context, key string) error {
	st, err := s.store.Get(ctx, key)
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("lockout: get: %w", err)
	}
	if !st.LockedUntil.IsZero() && s.clk.Now().Before(st.LockedUntil) {
		return gerr.Unauthorized("account locked")
	}
	return nil
}

// Fail records a failed attempt. When the count crosses MaxFails inside
// the window, the identity is locked for Cooldown.
func (s *Service) Fail(ctx context.Context, key string) error {
	now := s.clk.Now()
	st, err := s.store.Get(ctx, key)
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("lockout: get: %w", err)
	}
	if st.FirstFailAt.IsZero() || now.Sub(st.FirstFailAt) > s.opts.Window {
		st = State{FirstFailAt: now}
	}
	st.Fails++
	ttl := s.opts.Window
	if st.Fails >= s.opts.MaxFails {
		st.LockedUntil = now.Add(s.opts.Cooldown)
		ttl = s.opts.Cooldown
	}
	if setErr := s.store.Set(ctx, key, st, ttl); setErr != nil {
		return fmt.Errorf("lockout: set: %w", setErr)
	}
	return nil
}

// Reset clears the failure counter for key (call after a successful login).
func (s *Service) Reset(ctx context.Context, key string) error {
	if err := s.store.Delete(ctx, key); err != nil && !isNotFound(err) {
		return fmt.Errorf("lockout: delete: %w", err)
	}
	return nil
}

// MemoryStore is an in-process Store for tests / single-replica use.
type MemoryStore struct {
	mu   sync.Mutex
	data map[string]memEntry
}

type memEntry struct {
	state   State
	expires time.Time
}

// NewMemoryStore returns an initialised in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{data: make(map[string]memEntry)}
}

// Get returns the state or gerr.CodeNotFound.
func (m *MemoryStore) Get(_ context.Context, key string) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.data[key]
	if !ok {
		return State{}, gerr.NotFound("lockout: not found")
	}
	if !e.expires.IsZero() && time.Now().After(e.expires) {
		delete(m.data, key)
		return State{}, gerr.NotFound("lockout: expired")
	}
	return e.state, nil
}

// Set stores the state with a TTL.
func (m *MemoryStore) Set(_ context.Context, key string, s State, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = memEntry{state: s, expires: time.Now().Add(ttl)}
	return nil
}

// Delete removes the entry.
func (m *MemoryStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

func isNotFound(err error) bool {
	var e *gerr.Error
	return errors.As(err, &e) && e.Code == gerr.CodeNotFound
}
