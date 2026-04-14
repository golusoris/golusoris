package subs

import (
	"context"
	"sync"
)

// MemoryStore is an in-memory [Store] for tests and local dev.
type MemoryStore struct {
	mu   sync.RWMutex
	subs map[string]*Subscription
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{subs: map[string]*Subscription{}}
}

// Get implements [Store].
func (m *MemoryStore) Get(_ context.Context, id string) (*Subscription, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.subs[id]
	if !ok {
		return nil, ErrNotFound
	}
	// Return a copy so callers don't mutate the stored record.
	c := *s
	return &c, nil
}

// GetByCustomer implements [Store].
func (m *MemoryStore) GetByCustomer(_ context.Context, customerID string) ([]*Subscription, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []*Subscription
	for _, s := range m.subs {
		if s.CustomerID == customerID {
			c := *s
			out = append(out, &c)
		}
	}
	return out, nil
}

// Upsert implements [Store].
func (m *MemoryStore) Upsert(_ context.Context, s *Subscription) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := *s
	m.subs[c.ID] = &c
	return nil
}

// Delete implements [Store].
func (m *MemoryStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.subs, id)
	return nil
}
