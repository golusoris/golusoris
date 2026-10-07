// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package subs

import (
	"context"
	"maps"
	"sync"
	"time"
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
	return cloneSubscription(s), nil
}

// GetByCustomer implements [Store].
func (m *MemoryStore) GetByCustomer(_ context.Context, customerID string) ([]*Subscription, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []*Subscription
	for _, s := range m.subs {
		if s.CustomerID == customerID {
			out = append(out, cloneSubscription(s))
		}
	}
	return out, nil
}

// Upsert implements [Store].
func (m *MemoryStore) Upsert(_ context.Context, s *Subscription) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cloned := cloneSubscription(s)
	m.subs[cloned.ID] = cloned
	return nil
}

func cloneSubscription(source *Subscription) *Subscription {
	cloned := *source
	cloned.Metadata = maps.Clone(source.Metadata)
	cloned.TrialEndsAt = cloneTime(source.TrialEndsAt)
	cloned.CancelAt = cloneTime(source.CancelAt)
	cloned.CanceledAt = cloneTime(source.CanceledAt)
	cloned.PausedAt = cloneTime(source.PausedAt)
	return &cloned
}

func cloneTime(source *time.Time) *time.Time {
	if source == nil {
		return nil
	}
	cloned := *source
	return &cloned
}

// Delete implements [Store].
func (m *MemoryStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.subs, id)
	return nil
}
