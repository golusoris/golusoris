// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package metricdef

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/golusoris/golusoris/core/validate"
	"github.com/golusoris/golusoris/k8s/metrics/prom"
)

// Counter is a registered counter family.
type Counter struct {
	vec   *prometheus.CounterVec
	guard *labelGuard
}

// Add increments the series for labelValues by v (v >= 0). A sampled span in
// ctx becomes the exemplar.
func (c *Counter) Add(ctx context.Context, v float64, labelValues ...string) error {
	if v < 0 || math.IsNaN(v) {
		return fmt.Errorf("metricdef: %s: counter add %v: must be a non-negative number", c.guard.name, v)
	}
	values, err := c.guard.bound(labelValues)
	if err != nil {
		return err
	}
	m, err := c.vec.GetMetricWithLabelValues(values...)
	if err != nil {
		return fmt.Errorf("metricdef: %s: %w", c.guard.name, err)
	}
	if adder, ok := m.(prometheus.ExemplarAdder); ok {
		if ex := prom.ExemplarLabels(ctx); ex != nil {
			adder.AddWithExemplar(v, ex)
			return nil
		}
	}
	m.Add(v)
	return nil
}

// Inc adds 1.
func (c *Counter) Inc(ctx context.Context, labelValues ...string) error {
	return c.Add(ctx, 1, labelValues...)
}

// Gauge is a registered gauge family.
type Gauge struct {
	vec   *prometheus.GaugeVec
	guard *labelGuard
}

func (g *Gauge) series(labelValues []string) (prometheus.Gauge, error) {
	values, err := g.guard.bound(labelValues)
	if err != nil {
		return nil, err
	}
	m, err := g.vec.GetMetricWithLabelValues(values...)
	if err != nil {
		return nil, fmt.Errorf("metricdef: %s: %w", g.guard.name, err)
	}
	return m, nil
}

// Set sets the series for labelValues to v.
func (g *Gauge) Set(v float64, labelValues ...string) error {
	m, err := g.series(labelValues)
	if err != nil {
		return err
	}
	m.Set(v)
	return nil
}

// Add adds v (may be negative) to the series for labelValues.
func (g *Gauge) Add(v float64, labelValues ...string) error {
	m, err := g.series(labelValues)
	if err != nil {
		return err
	}
	m.Add(v)
	return nil
}

// Histogram is a registered histogram family.
type Histogram struct {
	vec   *prometheus.HistogramVec
	guard *labelGuard
}

// Observe records v for labelValues; a sampled span in ctx becomes the
// bucket's exemplar.
func (h *Histogram) Observe(ctx context.Context, v float64, labelValues ...string) error {
	values, err := h.guard.bound(labelValues)
	if err != nil {
		return err
	}
	m, err := h.vec.GetMetricWithLabelValues(values...)
	if err != nil {
		return fmt.Errorf("metricdef: %s: %w", h.guard.name, err)
	}
	prom.ObserveWithExemplar(ctx, m, v)
	return nil
}

// Handles holds the registered families of one [Register] call.
type Handles struct {
	counters   map[string]*Counter
	gauges     map[string]*Gauge
	histograms map[string]*Histogram
}

// Counter returns the counter named name.
func (h *Handles) Counter(name string) (*Counter, error) {
	if c, ok := h.counters[name]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("metricdef: no registered counter %q", name)
}

// Gauge returns the gauge named name.
func (h *Handles) Gauge(name string) (*Gauge, error) {
	if g, ok := h.gauges[name]; ok {
		return g, nil
	}
	return nil, fmt.Errorf("metricdef: no registered gauge %q", name)
}

// Histogram returns the histogram named name.
func (h *Handles) Histogram(name string) (*Histogram, error) {
	if hist, ok := h.histograms[name]; ok {
		return hist, nil
	}
	return nil, fmt.Errorf("metricdef: no registered histogram %q", name)
}

// Register validates defs, registers one family per def on reg and returns
// typed handles. It is all-or-nothing: on any failure the families it
// already registered are unregistered again. External defs are refused.
func Register(reg prometheus.Registerer, defs ...Def) (*Handles, error) {
	if validate.IsNil(reg) {
		return nil, errors.New("metricdef: register: nil registerer")
	}
	if _, err := NewCatalog(defs...); err != nil {
		return nil, fmt.Errorf("metricdef: register: %w", err)
	}
	h := &Handles{counters: map[string]*Counter{}, gauges: map[string]*Gauge{}, histograms: map[string]*Histogram{}}
	done := make([]prometheus.Collector, 0, len(defs))
	for _, d := range defs {
		c, err := h.add(d)
		if err == nil {
			err = reg.Register(c)
		}
		if err != nil {
			for _, registered := range done {
				reg.Unregister(registered)
			}
			return nil, fmt.Errorf("metricdef: register %s: %w", d.Name, err)
		}
		done = append(done, c)
	}
	return h, nil
}

// Register registers every non-External def of c on reg; see [Register].
func (c *Catalog) Register(reg prometheus.Registerer) (*Handles, error) {
	defs := make([]Def, 0, len(c.names))
	for _, d := range c.Defs() {
		if !d.External {
			defs = append(defs, d)
		}
	}
	return Register(reg, defs...)
}

func (h *Handles) add(d Def) (prometheus.Collector, error) {
	if d.External {
		return nil, errors.New("external metrics are emitted elsewhere")
	}
	guard := newLabelGuard(d)
	switch d.Kind {
	case KindCounter:
		vec := prometheus.NewCounterVec(prometheus.CounterOpts{Name: d.Name, Help: d.Help, Unit: d.Unit}, d.Labels)
		h.counters[d.Name] = &Counter{vec: vec, guard: guard}
		return vec, nil
	case KindGauge:
		vec := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: d.Name, Help: d.Help, Unit: d.Unit}, d.Labels)
		h.gauges[d.Name] = &Gauge{vec: vec, guard: guard}
		return vec, nil
	case KindHistogram:
		vec := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: d.Name, Help: d.Help, Unit: d.Unit, Buckets: d.Buckets}, d.Labels)
		h.histograms[d.Name] = &Histogram{vec: vec, guard: guard}
		return vec, nil
	case KindSummary:
	}
	return nil, fmt.Errorf("unknown kind %d", int(d.Kind))
}
