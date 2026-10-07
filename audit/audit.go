// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package audit provides an append-only structured audit log.
// Events record who did what to which resource, with optional before/after
// diff and arbitrary metadata.
//
// The Store interface is pluggable — ship with a Postgres implementation or
// use MemoryStore in tests.
//
// Usage:
//
//	logger := audit.New(store, audit.WithClock(clk))
//	_ = logger.Log(ctx, audit.Event{
//	    Actor:  "user:42",
//	    Action: "order.cancel",
//	    Target: "order:99",
//	    Diff:   audit.Diff{"status": {"pending", "cancelled"}},
//	})
package audit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/core/clock"
	"github.com/golusoris/golusoris/core/validate"
	"github.com/golusoris/golusoris/internal/snapshot"
)

// FieldChange captures a single field's before and after values.
type FieldChange struct {
	Before any
	After  any
}

// Diff is a map of field name → [before, after] for structured change recording.
type Diff map[string]FieldChange

// Event is a single immutable audit record.
type Event struct {
	// ID is a random hex string assigned by [Logger.Log] if empty.
	ID string
	// Actor is the identity that performed the action (e.g. "user:42", "service:worker").
	Actor string
	// Action is a dot-namespaced verb (e.g. "order.cancel", "user.update").
	Action string
	// Target is the resource affected (e.g. "order:99", "user:42").
	Target string
	// TenantID optionally scopes the event to a tenant.
	TenantID string
	// Diff records field-level changes. May be nil.
	Diff Diff
	// Metadata holds additional context (IP, user-agent, trace-id, …). May be nil.
	Metadata map[string]any
	// CreatedAt is set by [Logger.Log] when the event is appended.
	CreatedAt time.Time
}

// Filter restricts which events are returned by [Store.List].
type Filter struct {
	Actor    string
	Action   string
	Target   string
	TenantID string
	// After and Before are inclusive time bounds. Zero = unbounded.
	After  time.Time
	Before time.Time
	Limit  int64
	Offset int64
}

// Store persists audit events. Implementations must be safe for concurrent use.
type Store interface {
	// Append writes e to the audit log. e.ID and e.CreatedAt are already set.
	Append(ctx context.Context, e Event) error
	// List returns events matching filter, ordered by CreatedAt desc.
	List(ctx context.Context, f Filter) ([]Event, error)
}

// Option configures a Logger.
type Option func(*Logger)

// WithClock overrides the clock used to stamp events. Default: real clock.
func WithClock(c clock.Clock) Option {
	return func(l *Logger) {
		if !validate.IsNil(c) {
			l.clk = c
		}
	}
}

// Logger appends audit events via the configured Store.
type Logger struct {
	store Store
	clk   clock.Clock
}

// New returns a Logger backed by store.
func New(store Store, opts ...Option) *Logger {
	if validate.IsNil(store) {
		store = nil
	}
	l := &Logger{store: store, clk: clockwork.NewRealClock()}
	for _, o := range opts {
		if o != nil {
			o(l)
		}
	}
	return l
}

// Log appends e to the audit log, assigning ID and CreatedAt if unset.
func (l *Logger) Log(ctx context.Context, e Event) error {
	if l.store == nil {
		return errors.New("audit: store is required")
	}
	if e.ID == "" {
		id, err := newID()
		if err != nil {
			return err
		}
		e.ID = id
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = l.clk.Now()
	}
	if err := l.store.Append(ctx, e); err != nil {
		return fmt.Errorf("audit: append: %w", err)
	}
	return nil
}

// List returns events matching filter via the underlying store.
func (l *Logger) List(ctx context.Context, f Filter) ([]Event, error) {
	if l.store == nil {
		return nil, errors.New("audit: store is required")
	}
	out, err := l.store.List(ctx, f)
	if err != nil {
		return nil, fmt.Errorf("audit: list: %w", err)
	}
	return out, nil
}

// MemoryStore is a thread-safe in-memory audit store for tests.
type MemoryStore struct {
	mu     chan struct{} // mutex via buffered channel
	events []Event
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	mu := make(chan struct{}, 1)
	mu <- struct{}{}
	return &MemoryStore{mu: mu}
}

// Append implements [Store].
func (s *MemoryStore) Append(_ context.Context, e Event) error {
	snapshot, err := cloneEvent(e)
	if err != nil {
		return fmt.Errorf("audit: snapshot event: %w", err)
	}
	<-s.mu
	defer func() { s.mu <- struct{}{} }()
	s.events = append(s.events, snapshot)
	return nil
}

// List implements [Store]. Returns events newest-first, filtered by f.
func (s *MemoryStore) List(_ context.Context, f Filter) ([]Event, error) {
	<-s.mu
	defer func() { s.mu <- struct{}{} }()

	var out []Event
	var skipped int64
	// Iterate newest-first.
	for _, e := range slices.Backward(s.events) {
		if !matchesFilter(e, f) {
			continue
		}
		if skipped < max(f.Offset, 0) {
			skipped++
			continue
		}
		cloned, err := cloneEvent(e)
		if err != nil {
			return nil, fmt.Errorf("audit: clone stored event: %w", err)
		}
		out = append(out, cloned)
		if f.Limit > 0 && int64(len(out)) >= f.Limit {
			break
		}
	}
	return out, nil
}

// matchesFilter reports whether e satisfies every non-zero-value criterion
// of f. A zero-value field (empty string, zero time) is unconstrained.
func matchesFilter(e Event, f Filter) bool {
	return matchesIdentity(e, f) && matchesTimeRange(e, f)
}

// matchesIdentity checks f's non-empty Actor/Action/Target/TenantID filters
// against e.
func matchesIdentity(e Event, f Filter) bool {
	if f.Actor != "" && e.Actor != f.Actor {
		return false
	}
	if f.Action != "" && e.Action != f.Action {
		return false
	}
	if f.Target != "" && e.Target != f.Target {
		return false
	}
	if f.TenantID != "" && e.TenantID != f.TenantID {
		return false
	}
	return true
}

// matchesTimeRange checks e.CreatedAt against f's non-zero After/Before
// bounds.
func matchesTimeRange(e Event, f Filter) bool {
	if !f.After.IsZero() && e.CreatedAt.Before(f.After) {
		return false
	}
	if !f.Before.IsZero() && e.CreatedAt.After(f.Before) {
		return false
	}
	return true
}

// All returns snapshots of all stored events in insertion order. It preserves
// the legacy no-error API and fails closed with a nil snapshot if internal
// state cannot be cloned.
func (s *MemoryStore) All() []Event {
	events, err := s.AllChecked()
	if err != nil {
		return nil
	}
	return events
}

// AllChecked returns snapshots of all stored events in insertion order and
// reports snapshot validation failures.
func (s *MemoryStore) AllChecked() ([]Event, error) {
	<-s.mu
	defer func() { s.mu <- struct{}{} }()
	cp := make([]Event, len(s.events))
	for i := range s.events {
		cloned, err := cloneEvent(s.events[i])
		if err != nil {
			return nil, fmt.Errorf("audit: clone stored event: %w", err)
		}
		cp[i] = cloned
	}
	return cp, nil
}

func cloneEvent(event Event) (Event, error) {
	diff, err := snapshot.Clone(event.Diff)
	if err != nil {
		return Event{}, fmt.Errorf("audit: snapshot diff: %w", err)
	}
	metadata, err := snapshot.Clone(event.Metadata)
	if err != nil {
		return Event{}, fmt.Errorf("audit: snapshot metadata: %w", err)
	}
	event.Diff = diff
	event.Metadata = metadata
	return event, nil
}

// --- helpers ---

func newID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("audit: rand.Read: %w", err)
	}
	return hex.EncodeToString(b), nil
}
