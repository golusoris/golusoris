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
	"fmt"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/golusoris/golusoris/clock"
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
func WithClock(c clock.Clock) Option { return func(l *Logger) { l.clk = c } }

// Logger appends audit events via the configured Store.
type Logger struct {
	store Store
	clk   clock.Clock
}

// New returns a Logger backed by store.
func New(store Store, opts ...Option) *Logger {
	l := &Logger{store: store, clk: clockwork.NewRealClock()}
	for _, o := range opts {
		o(l)
	}
	return l
}

// Log appends e to the audit log, assigning ID and CreatedAt if unset.
func (l *Logger) Log(ctx context.Context, e Event) error {
	if e.ID == "" {
		e.ID = newID()
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = l.clk.Now()
	}
	return l.store.Append(ctx, e)
}

// List returns events matching filter via the underlying store.
func (l *Logger) List(ctx context.Context, f Filter) ([]Event, error) {
	return l.store.List(ctx, f)
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

func (s *MemoryStore) Append(_ context.Context, e Event) error {
	<-s.mu
	defer func() { s.mu <- struct{}{} }()
	s.events = append(s.events, e)
	return nil
}

func (s *MemoryStore) List(_ context.Context, f Filter) ([]Event, error) {
	<-s.mu
	defer func() { s.mu <- struct{}{} }()

	var out []Event
	// Iterate newest-first.
	for i := len(s.events) - 1; i >= 0; i-- {
		e := s.events[i]
		if f.Actor != "" && e.Actor != f.Actor {
			continue
		}
		if f.Action != "" && e.Action != f.Action {
			continue
		}
		if f.Target != "" && e.Target != f.Target {
			continue
		}
		if f.TenantID != "" && e.TenantID != f.TenantID {
			continue
		}
		if !f.After.IsZero() && !e.CreatedAt.After(f.After) {
			continue
		}
		if !f.Before.IsZero() && !e.CreatedAt.Before(f.Before) {
			continue
		}
		out = append(out, e)
		if f.Limit > 0 && int64(len(out)) >= f.Limit {
			break
		}
	}
	return out, nil
}

// All returns all stored events in insertion order (for test assertions).
func (s *MemoryStore) All() []Event {
	<-s.mu
	defer func() { s.mu <- struct{}{} }()
	cp := make([]Event, len(s.events))
	copy(cp, s.events)
	return cp
}

// --- helpers ---

func newID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("audit: rand.Read: %v", err))
	}
	return hex.EncodeToString(b)
}

