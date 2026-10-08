// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package otel

import (
	"context"
	"errors"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/golusoris/golusoris/core/validate"
)

// newPrometheusReader registers an OTel→Prometheus collector on reg (nil:
// prometheus.DefaultRegisterer). Names follow the exporter default
// (underscore escaping with unit and _total suffixes), matching what an OTel
// Collector or Prometheus OTLP ingestion produces from the same instruments.
func newPrometheusReader(ctx context.Context, reg prometheus.Registerer) (sdkmetric.Reader, error) {
	if validate.IsNil(reg) {
		reg = prometheus.DefaultRegisterer
	}
	exp, err := otelprom.New(otelprom.WithRegisterer(reg))
	if err == nil {
		return exp, nil
	}
	buildErr := fmt.Errorf("otel: prometheus exporter: %w", err)
	if exp == nil {
		return nil, buildErr
	}
	// The collector is registered already; a shut-down reader makes it silent.
	return nil, errors.Join(buildErr, shutdownReader(ctx, exp))
}

// shutdownReader stops a reader that never reached a meter provider; nil is
// a no-op.
func shutdownReader(ctx context.Context, r sdkmetric.Reader) error {
	if r == nil {
		return nil
	}
	if err := r.Shutdown(ctx); err != nil {
		return fmt.Errorf("otel: prometheus reader shutdown: %w", err)
	}
	return nil
}
