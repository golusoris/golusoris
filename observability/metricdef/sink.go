// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package metricdef

import (
	"context"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/golusoris/golusoris/k8s/metrics/prom"
)

type counterSink interface {
	add(context.Context, float64, []string) error
}

type gaugeSink interface {
	set(float64, []string) error
	add(float64, []string) error
}

type histogramSink interface {
	observe(context.Context, float64, []string) error
}

type prometheusCounter struct {
	vec *prometheus.CounterVec
}

func (s prometheusCounter) add(ctx context.Context, v float64, values []string) error {
	m, err := s.vec.GetMetricWithLabelValues(values...)
	if err != nil {
		return fmt.Errorf("get series: %w", err)
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

type prometheusGauge struct {
	vec *prometheus.GaugeVec
}

func (s prometheusGauge) set(v float64, values []string) error {
	m, err := s.vec.GetMetricWithLabelValues(values...)
	if err != nil {
		return fmt.Errorf("get series: %w", err)
	}
	m.Set(v)
	return nil
}

func (s prometheusGauge) add(v float64, values []string) error {
	m, err := s.vec.GetMetricWithLabelValues(values...)
	if err != nil {
		return fmt.Errorf("get series: %w", err)
	}
	m.Add(v)
	return nil
}

type prometheusHistogram struct {
	vec *prometheus.HistogramVec
}

func (s prometheusHistogram) observe(ctx context.Context, v float64, values []string) error {
	m, err := s.vec.GetMetricWithLabelValues(values...)
	if err != nil {
		return fmt.Errorf("get series: %w", err)
	}
	prom.ObserveWithExemplar(ctx, m, v)
	return nil
}
