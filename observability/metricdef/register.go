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
)

// Counter is a registered counter family.
type Counter struct {
	sink  counterSink
	guard *labelGuard
}

// Add increments the series for labelValues by v (v >= 0). A sampled span in
// ctx becomes the exemplar on the Prometheus backend.
func (c *Counter) Add(ctx context.Context, v float64, labelValues ...string) error {
	if v < 0 || math.IsNaN(v) {
		return fmt.Errorf("metricdef: %s: counter add %v: must be a non-negative number", c.guard.name, v)
	}
	values, err := c.guard.bound(labelValues)
	if err != nil {
		return err
	}
	if err = c.sink.add(ctx, v, values); err != nil {
		return fmt.Errorf("metricdef: %s: %w", c.guard.name, err)
	}
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

// Set sets the series for labelValues to v.
func (g *Gauge) Set(v float64, labelValues ...string) error {
	values, err := g.guard.bound(labelValues)
	if err != nil {
		return err
	}
	m, err := g.vec.GetMetricWithLabelValues(values...)
	if err != nil {
		return fmt.Errorf("metricdef: %s: get series: %w", g.guard.name, err)
	}
	m.Set(v)
	return nil
}

// Add adds v (may be negative) to the series for labelValues.
func (g *Gauge) Add(v float64, labelValues ...string) error {
	values, err := g.guard.bound(labelValues)
	if err != nil {
		return err
	}
	m, err := g.vec.GetMetricWithLabelValues(values...)
	if err != nil {
		return fmt.Errorf("metricdef: %s: get series: %w", g.guard.name, err)
	}
	m.Add(v)
	return nil
}

// Reset deletes all metrics in the gauge family and clears all distinct label guards.
func (g *Gauge) Reset() {
	g.vec.Reset()
	g.guard.reset()
}

// Delete deletes the series for labelValues and reports whether one was
// deleted. A value its guard would fold into [OtherValue] returns false, so
// the shared overflow series is never deleted by accident; pass [OtherValue]
// to delete that series. Guard slots stay taken; see [Gauge.Reset].
func (g *Gauge) Delete(labelValues ...string) bool {
	if !g.guard.known(labelValues) {
		return false
	}
	return g.vec.DeleteLabelValues(labelValues...)
}

// Histogram is a registered histogram family.
type Histogram struct {
	sink  histogramSink
	guard *labelGuard
}

// Observe records v for labelValues; a sampled span in ctx becomes the
// bucket's exemplar on the Prometheus backend.
func (h *Histogram) Observe(ctx context.Context, v float64, labelValues ...string) error {
	values, err := h.guard.bound(labelValues)
	if err != nil {
		return err
	}
	if err = h.sink.observe(ctx, v, values); err != nil {
		return fmt.Errorf("metricdef: %s: %w", h.guard.name, err)
	}
	return nil
}

// Handles holds the registered families of one [Register], [RegisterOrReuse] or [RegisterOTel] call.
type Handles struct {
	counters   map[string]*Counter
	gauges     map[string]*Gauge
	otelGauges map[string]*OTelGauge
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
	if _, ok := h.otelGauges[name]; ok {
		return nil, fmt.Errorf("metricdef: %q is an OTel gauge; use OTelGauge", name)
	}
	return nil, fmt.Errorf("metricdef: no registered gauge %q", name)
}

// OTelGauge returns the OTel gauge named name.
func (h *Handles) OTelGauge(name string) (*OTelGauge, error) {
	if g, ok := h.otelGauges[name]; ok {
		return g, nil
	}
	if _, ok := h.gauges[name]; ok {
		return nil, fmt.Errorf("metricdef: %q is a Prometheus gauge; use Gauge", name)
	}
	return nil, fmt.Errorf("metricdef: no registered OTel gauge %q", name)
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
	return register(reg, defs, false)
}

// RegisterOrReuse validates defs, registers each family or reuses an already
// registered family on reg and returns typed handles. It is all-or-nothing:
// on failure, only collectors newly registered in this call are unregistered.
// Reused handles keep separate guards, so process-wide cardinality per family
// is at most handles x limit. A reused histogram keeps the buckets of the
// first registration. External defs are refused.
func RegisterOrReuse(reg prometheus.Registerer, defs ...Def) (*Handles, error) {
	return register(reg, defs, true)
}

// Register registers every non-External def of c on reg; see [Register].
func (c *Catalog) Register(reg prometheus.Registerer) (*Handles, error) {
	return Register(reg, c.registerDefs()...)
}

// RegisterOrReuse registers or reuses every non-External def of c on reg; see [RegisterOrReuse].
func (c *Catalog) RegisterOrReuse(reg prometheus.Registerer) (*Handles, error) {
	return RegisterOrReuse(reg, c.registerDefs()...)
}

func (c *Catalog) registerDefs() []Def {
	defs := make([]Def, 0, len(c.names))
	for _, d := range c.Defs() {
		if !d.External {
			defs = append(defs, d)
		}
	}
	return defs
}

func register(reg prometheus.Registerer, defs []Def, reuse bool) (*Handles, error) {
	if validate.IsNil(reg) {
		return nil, errors.New("metricdef: register: nil registerer")
	}
	if _, err := NewCatalog(defs...); err != nil {
		return nil, fmt.Errorf("metricdef: register: %w", err)
	}
	h := &Handles{
		counters:   make(map[string]*Counter, len(defs)),
		gauges:     make(map[string]*Gauge, len(defs)),
		otelGauges: make(map[string]*OTelGauge),
		histograms: make(map[string]*Histogram, len(defs)),
	}
	done := make([]prometheus.Collector, 0, len(defs))
	for _, d := range defs {
		c, err := h.add(d)
		if err == nil {
			err = reg.Register(c)
		}
		if err != nil {
			var are prometheus.AlreadyRegisteredError
			if reuse && errors.As(err, &are) {
				if rerr := h.reuse(d, are.ExistingCollector); rerr != nil {
					rollback(reg, done)
					return nil, rerr
				}
				continue
			}
			rollback(reg, done)
			return nil, fmt.Errorf("metricdef: register %s: %w", d.Name, err)
		}
		done = append(done, c)
	}
	return h, nil
}

func rollback(reg prometheus.Registerer, done []prometheus.Collector) {
	for _, registered := range done {
		reg.Unregister(registered)
	}
}

func (h *Handles) add(d Def) (prometheus.Collector, error) {
	if d.External {
		return nil, errors.New("external metrics are emitted elsewhere")
	}
	guard := newLabelGuard(d)
	switch d.Kind {
	case KindCounter:
		vec := prometheus.NewCounterVec(prometheus.CounterOpts{Name: d.Name, Help: d.Help, Unit: d.Unit}, d.Labels)
		h.counters[d.Name] = &Counter{sink: prometheusCounter{vec: vec}, guard: guard}
		return vec, nil
	case KindGauge:
		vec := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: d.Name, Help: d.Help, Unit: d.Unit}, d.Labels)
		h.gauges[d.Name] = &Gauge{vec: vec, guard: guard}
		return vec, nil
	case KindHistogram:
		vec := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: d.Name, Help: d.Help, Unit: d.Unit, Buckets: d.Buckets}, d.Labels)
		h.histograms[d.Name] = &Histogram{sink: prometheusHistogram{vec: vec}, guard: guard}
		return vec, nil
	case KindSummary:
	}
	return nil, fmt.Errorf("unknown kind %d", int(d.Kind))
}

func (h *Handles) reuse(d Def, existing prometheus.Collector) error {
	guard := newLabelGuard(d)
	switch d.Kind {
	case KindCounter:
		vec, ok := existing.(*prometheus.CounterVec)
		if !ok {
			return fmt.Errorf("metricdef: register %s: collector type mismatch: existing collector is %T, want *prometheus.CounterVec", d.Name, existing)
		}
		h.counters[d.Name] = &Counter{sink: prometheusCounter{vec: vec}, guard: guard}
		return nil
	case KindGauge:
		vec, ok := existing.(*prometheus.GaugeVec)
		if !ok {
			return fmt.Errorf("metricdef: register %s: collector type mismatch: existing collector is %T, want *prometheus.GaugeVec", d.Name, existing)
		}
		h.gauges[d.Name] = &Gauge{vec: vec, guard: guard}
		return nil
	case KindHistogram:
		vec, ok := existing.(*prometheus.HistogramVec)
		if !ok {
			return fmt.Errorf("metricdef: register %s: collector type mismatch: existing collector is %T, want *prometheus.HistogramVec", d.Name, existing)
		}
		h.histograms[d.Name] = &Histogram{sink: prometheusHistogram{vec: vec}, guard: guard}
		return nil
	case KindSummary:
	}
	return fmt.Errorf("metricdef: register %s: unknown kind %d", d.Name, int(d.Kind))
}
