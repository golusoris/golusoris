// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package metricdef

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Catalog is an immutable, validated set of [Def] values: the single source
// services register from and generators read.
type Catalog struct {
	defs     map[string]Def
	series   map[string]string // exposed sample name -> def name
	families map[string]string // OpenMetrics family name -> def name
	names    []string          // sorted def names
}

// NewCatalog validates defs and rejects duplicate names, including
// OpenMetrics family clashes between non-External defs (counter "x_total" is
// family "x", so it cannot sit next to a gauge "x"). Classic series of
// non-External defs cannot collide otherwise: [Def.Validate] reserves
// _bucket/_count/_sum and _total.
func NewCatalog(defs ...Def) (*Catalog, error) {
	c := &Catalog{
		defs:     make(map[string]Def, len(defs)),
		series:   make(map[string]string, len(defs)*3),
		families: make(map[string]string, len(defs)),
		names:    make([]string, 0, len(defs)),
	}
	var errs []error
	for _, d := range defs {
		if err := c.add(d); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	slices.Sort(c.names)
	return c, nil
}

func (c *Catalog) add(d Def) error {
	if err := d.Validate(); err != nil {
		return err
	}
	if _, dup := c.defs[d.Name]; dup {
		return fmt.Errorf("metricdef: duplicate metric %q", d.Name)
	}
	if err := c.claimFamily(d); err != nil {
		return err
	}
	for _, s := range d.Series() {
		if owner, taken := c.series[s]; taken {
			return fmt.Errorf("metricdef: series %q of %q is already exposed by %q", s, d.Name, owner)
		}
	}
	for _, s := range d.Series() {
		c.series[s] = d.Name
	}
	c.defs[d.Name] = d.clone()
	c.names = append(c.names, d.Name)
	return nil
}

// claimFamily rejects OpenMetrics family clashes between defs this catalog
// owns; External emitters (the Go collector exposes go_memstats_alloc_bytes
// and go_memstats_alloc_bytes_total) are taken as they are.
func (c *Catalog) claimFamily(d Def) error {
	if d.External {
		return nil
	}
	family := strings.TrimSuffix(d.Name, "_total")
	if owner, dup := c.families[family]; dup {
		return fmt.Errorf("metricdef: metric %q duplicates family %q of %q", d.Name, family, owner)
	}
	c.families[family] = d.Name
	return nil
}

// Defs returns deep copies of every def, sorted by name.
func (c *Catalog) Defs() []Def {
	out := make([]Def, 0, len(c.names))
	for _, n := range c.names {
		out = append(out, c.defs[n].clone())
	}
	return out
}

// Lookup returns the def named name.
func (c *Catalog) Lookup(name string) (Def, bool) {
	d, ok := c.defs[name]
	if !ok {
		return Def{}, false
	}
	return d.clone(), true
}

// LookupSeries resolves an exposed sample name ("x_bucket", "x_total") to
// its def.
func (c *Catalog) LookupSeries(series string) (Def, bool) {
	name, ok := c.series[series]
	if !ok {
		return Def{}, false
	}
	return c.Lookup(name)
}

// Series returns every exposed sample name, sorted.
func (c *Catalog) Series() []string {
	out := make([]string, 0, len(c.series))
	for s := range c.series {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// Merge returns a catalog holding c's defs plus others' defs; collisions fail.
func (c *Catalog) Merge(others ...*Catalog) (*Catalog, error) {
	all := c.Defs()
	for _, o := range others {
		all = append(all, o.Defs()...)
	}
	merged, err := NewCatalog(all...)
	if err != nil {
		return nil, fmt.Errorf("metricdef: merge: %w", err)
	}
	return merged, nil
}

// String lists the def names; useful in test failure messages.
func (c *Catalog) String() string {
	return "metricdef.Catalog[" + strings.Join(c.names, " ") + "]"
}
