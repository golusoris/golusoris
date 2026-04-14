package meter

import (
	"context"
	"sort"
	"sync"
)

// MemoryStore is an in-memory [Store] for tests.
type MemoryStore struct {
	mu     sync.RWMutex
	byID   map[string]Event
	sorted []Event // ordered by Event.At asc
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{byID: map[string]Event{}}
}

// Insert implements [Store].
func (m *MemoryStore) Insert(_ context.Context, e Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[e.ID]; ok {
		return ErrDuplicate
	}
	m.byID[e.ID] = e
	m.sorted = append(m.sorted, e)
	sort.Slice(m.sorted, func(i, j int) bool { return m.sorted[i].At.Before(m.sorted[j].At) })
	return nil
}

// Query implements [Store].
func (m *MemoryStore) Query(_ context.Context, f Filter) ([]Event, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Event
	for _, e := range m.sorted {
		if !match(e, f) {
			continue
		}
		out = append(out, e)
		if f.Limit > 0 && len(out) >= f.Limit {
			break
		}
	}
	return out, nil
}

// Sum implements [Store].
func (m *MemoryStore) Sum(_ context.Context, f Filter) (float64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var total float64
	for _, e := range m.sorted {
		if match(e, f) {
			total += e.Quantity
		}
	}
	return total, nil
}

func match(e Event, f Filter) bool {
	if f.CustomerID != "" && e.CustomerID != f.CustomerID {
		return false
	}
	if f.Meter != "" && e.Meter != f.Meter {
		return false
	}
	if !f.Since.IsZero() && e.At.Before(f.Since) {
		return false
	}
	if !f.Until.IsZero() && !e.At.Before(f.Until) {
		return false
	}
	return true
}
