// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package metricdef

import (
	"fmt"
	"sync"
)

// valueGuard maps one label value to itself or [OtherValue].
type valueGuard interface {
	admit(value string) string
}

type allowGuard map[string]struct{}

func (g allowGuard) admit(value string) string {
	if _, ok := g[value]; ok {
		return value
	}
	return OtherValue
}

// distinctGuard admits the first max distinct values; memory stays bounded
// by max.
type distinctGuard struct {
	mu   sync.RWMutex
	seen map[string]struct{}
	max  int
}

func (g *distinctGuard) admit(value string) string {
	g.mu.RLock()
	_, known := g.seen[value]
	g.mu.RUnlock()
	if known {
		return value
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.seen[value]; ok {
		return value
	}
	if len(g.seen) >= g.max {
		return OtherValue
	}
	g.seen[value] = struct{}{}
	return value
}

// labelGuard checks label counts and applies per-label limits.
type labelGuard struct {
	name   string
	count  int
	guards []valueGuard // index-aligned with Def.Labels; nil = unbounded
}

func newLabelGuard(d Def) *labelGuard {
	lg := &labelGuard{name: d.Name, count: len(d.Labels), guards: make([]valueGuard, len(d.Labels))}
	for i, l := range d.Labels {
		if limit, ok := d.Limits[l]; ok {
			lg.guards[i] = newValueGuard(limit)
		}
	}
	return lg
}

func newValueGuard(limit LabelLimit) valueGuard {
	if len(limit.Allow) == 0 {
		return &distinctGuard{seen: make(map[string]struct{}, limit.MaxDistinct), max: limit.MaxDistinct}
	}
	allow := make(allowGuard, len(limit.Allow))
	for _, v := range limit.Allow {
		allow[v] = struct{}{}
	}
	return allow
}

// bound returns values with limited labels folded; the caller's slice is
// copied before any replacement.
func (lg *labelGuard) bound(values []string) ([]string, error) {
	if len(values) != lg.count {
		return nil, fmt.Errorf("metricdef: %s: got %d label values, want %d", lg.name, len(values), lg.count)
	}
	out, copied := values, false
	for i, g := range lg.guards {
		if g == nil {
			continue
		}
		admitted := g.admit(values[i])
		if admitted == values[i] {
			continue
		}
		if !copied {
			out, copied = append([]string(nil), values...), true
		}
		out[i] = admitted
	}
	return out, nil
}
