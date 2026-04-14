package invoice

import (
	"context"
	"strconv"
	"strings"
	"sync"
)

// MemoryNumberer is a per-tenant sequential numberer for tests + dev.
// Format: <prefix>-<zero-padded-counter>.
type MemoryNumberer struct {
	prefix    string
	width     int
	mu        sync.Mutex
	counters  map[string]uint64
	separator string
}

// NewMemoryNumberer returns an in-memory Numberer with the given
// prefix and zero-pad width (e.g. width=6 → "000001"). The separator
// between prefix and counter defaults to "-".
func NewMemoryNumberer(prefix string, width int) *MemoryNumberer {
	if width < 1 {
		width = 1
	}
	return &MemoryNumberer{
		prefix:    prefix,
		width:     width,
		counters:  map[string]uint64{},
		separator: "-",
	}
}

// SetSeparator overrides the prefix/counter separator.
func (m *MemoryNumberer) SetSeparator(s string) { m.separator = s }

// Next implements [Numberer]. Counters are per-tenant.
func (m *MemoryNumberer) Next(_ context.Context, tenantID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counters[tenantID]++
	n := m.counters[tenantID]
	prefix := m.prefix
	if prefix == "" {
		return padN(n, m.width), nil
	}
	return prefix + m.separator + padN(n, m.width), nil
}

func padN(n uint64, width int) string {
	s := strconv.FormatUint(n, 10)
	if len(s) >= width {
		return s
	}
	return strings.Repeat("0", width-len(s)) + s
}

// Reset clears a tenant's counter (test helper). Production
// numberers must never reset — reuse breaks audit trails.
func (m *MemoryNumberer) Reset(tenantID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.counters, tenantID)
}
