// Package otel wires OpenTelemetry — tracer, meter, and logger — with an
// OTLP gRPC exporter. The SDK registers itself as the global OTel provider
// so [httpx/middleware.OTel] and [httpx/client] produce spans automatically.
//
// A slog-bridge is registered via [otelslog.NewHandler] so every slog call
// is also emitted as an OTel log record. Apps that wire both
// [golusoris/log] and [otel.Module] get HTTP access logs, app logs, spans,
// and metrics on the same export pipeline.
//
// Config keys (env: APP_OTEL_*):
//
//	otel.enabled             # master switch (default true)
//	otel.endpoint            # OTLP gRPC target, e.g. "otel-collector:4317"
//	otel.insecure            # plaintext gRPC (default true — collectors
//	                         # usually live in-cluster)
//	otel.service.name        # service.name resource attr (required)
//	otel.service.version     # service.version
//	otel.service.namespace   # service.namespace
//	otel.sample.ratio        # parent-based ratio sampler, 0-1 (default 1.0)
//	otel.export.traces       # enable trace export (default true)
//	otel.export.metrics      # enable metric export (default true)
//	otel.export.logs         # enable log export (default true)
//
// When otel.enabled=false the module installs a no-op tracer/meter/logger
// so app code using the OTel API costs nothing.
package otel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	otelapi "go.opentelemetry.io/otel"
	otlplog "go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	otlpmetric "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	otlptrace "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.uber.org/fx"

	"github.com/golusoris/golusoris/config"
)

// Options configures the OTel SDK.
type Options struct {
	Enabled  bool           `koanf:"enabled"`
	Endpoint string         `koanf:"endpoint"`
	Insecure bool           `koanf:"insecure"`
	Service  ServiceOptions `koanf:"service"`
	Sample   SampleOptions  `koanf:"sample"`
	Export   ExportOptions  `koanf:"export"`
}

// ServiceOptions identifies the service in OTel resource attributes.
type ServiceOptions struct {
	Name      string `koanf:"name"`
	Version   string `koanf:"version"`
	Namespace string `koanf:"namespace"`
}

// SampleOptions tunes trace sampling.
type SampleOptions struct {
	Ratio float64 `koanf:"ratio"`
}

// ExportOptions toggles per-signal export. Useful when a collector only
// accepts one signal type or an app is noisy in one dimension.
type ExportOptions struct {
	Traces  bool `koanf:"traces"`
	Metrics bool `koanf:"metrics"`
	Logs    bool `koanf:"logs"`
}

// DefaultOptions returns the opinionated defaults.
func DefaultOptions() Options {
	return Options{
		Enabled:  true,
		Insecure: true,
		Sample:   SampleOptions{Ratio: 1.0},
		Export:   ExportOptions{Traces: true, Metrics: true, Logs: true},
	}
}

// Providers bundles the three signal providers so Shutdown can tear them
// all down in one call. Apps can inject this if they need the underlying
// SDK handles (most don't — use the global API via otel.Tracer / Meter /
// slog.Default).
type Providers struct {
	Tracer *sdktrace.TracerProvider
	Meter  *sdkmetric.MeterProvider
	Logger *sdklog.LoggerProvider
}

// Shutdown flushes + stops the providers. Errors from individual
// providers are joined so one failure doesn't hide the others.
func (p *Providers) Shutdown(ctx context.Context) error {
	var errs []error
	if p.Tracer != nil {
		if err := p.Tracer.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("otel: tracer shutdown: %w", err))
		}
	}
	if p.Meter != nil {
		if err := p.Meter.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("otel: meter shutdown: %w", err))
		}
	}
	if p.Logger != nil {
		if err := p.Logger.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("otel: logger shutdown: %w", err))
		}
	}
	return errors.Join(errs...)
}

// New constructs the providers, registers them as OTel globals, installs the
// W3C TraceContext + Baggage propagator, and returns a Providers handle.
// Apps usually don't call this directly — use [Module].
func New(ctx context.Context, opts Options) (*Providers, error) {
	if !opts.Enabled {
		return &Providers{}, nil
	}
	res, err := buildResource(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("otel: resource: %w", err)
	}

	providers := &Providers{}

	if opts.Export.Traces {
		p, perr := buildTracerProvider(ctx, res, opts)
		if perr != nil {
			return nil, perr
		}
		providers.Tracer = p
		otelapi.SetTracerProvider(p)
	}
	if opts.Export.Metrics {
		p, perr := buildMeterProvider(ctx, res, opts)
		if perr != nil {
			return nil, perr
		}
		providers.Meter = p
		otelapi.SetMeterProvider(p)
	}
	if opts.Export.Logs {
		p, perr := buildLoggerProvider(ctx, res, opts)
		if perr != nil {
			return nil, perr
		}
		providers.Logger = p
		global.SetLoggerProvider(p)
	}

	otelapi.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return providers, nil
}

func buildResource(ctx context.Context, opts Options) (*resource.Resource, error) {
	attrs := make([]attributeKV, 0, 3+8)
	attrs = append(attrs,
		attributeKV{semconv.ServiceNameKey, opts.Service.Name},
		attributeKV{semconv.ServiceVersionKey, opts.Service.Version},
		attributeKV{semconv.ServiceNamespaceKey, opts.Service.Namespace},
	)
	// Pull pod metadata from the k8s downward API (matches log package convention).
	attrs = append(attrs, podInfoAttrs()...)

	resOpts := []resource.Option{
		resource.WithFromEnv(),
		resource.WithProcess(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(toKVs(attrs)...),
	}
	res, err := resource.New(ctx, resOpts...)
	if err != nil {
		return nil, fmt.Errorf("resource.New: %w", err)
	}
	return res, nil
}

func buildTracerProvider(ctx context.Context, res *resource.Resource, opts Options) (*sdktrace.TracerProvider, error) {
	exp, err := otlptrace.New(ctx,
		dialOpts(opts)...,
	)
	if err != nil {
		return nil, fmt.Errorf("otel: trace exporter: %w", err)
	}
	return sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp, sdktrace.WithBatchTimeout(5*time.Second)),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(opts.Sample.Ratio))),
	), nil
}

func buildMeterProvider(ctx context.Context, res *resource.Resource, opts Options) (*sdkmetric.MeterProvider, error) {
	exp, err := otlpmetric.New(ctx, metricDialOpts(opts)...)
	if err != nil {
		return nil, fmt.Errorf("otel: metric exporter: %w", err)
	}
	return sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(15*time.Second))),
		sdkmetric.WithResource(res),
	), nil
}

func buildLoggerProvider(ctx context.Context, res *resource.Resource, opts Options) (*sdklog.LoggerProvider, error) {
	exp, err := otlplog.New(ctx, logDialOpts(opts)...)
	if err != nil {
		return nil, fmt.Errorf("otel: log exporter: %w", err)
	}
	return sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)),
		sdklog.WithResource(res),
	), nil
}

// loadOptions unmarshals config into Options; Service.Name is required when
// Enabled=true.
func loadOptions(cfg *config.Config) (Options, error) {
	opts := DefaultOptions()
	if err := cfg.Unmarshal("otel", &opts); err != nil {
		return Options{}, fmt.Errorf("otel: load options: %w", err)
	}
	if opts.Enabled && opts.Service.Name == "" {
		return Options{}, errors.New("otel: otel.service.name is required when otel is enabled")
	}
	return opts, nil
}

// ModuleWithSlogBridge adds the slog → OTel logs bridge to the default
// slog logger. Apps that want every slog call also exported as an OTel log
// record include this module (in addition to [Module]).
//
// Kept separate from [Module] because some apps use slog but route logs
// through Sentry + stdout only, not OTel.
var ModuleWithSlogBridge = fx.Module("golusoris.otel.slog_bridge",
	fx.Invoke(func(providers *Providers, existing *slog.Logger, opts Options) {
		if providers == nil || providers.Logger == nil {
			return
		}
		// Fan out to the existing handler + the OTel bridge.
		otelHandler := otelslog.NewHandler(opts.Service.Name,
			otelslog.WithLoggerProvider(providers.Logger),
		)
		slog.SetDefault(slog.New(&fanoutHandler{
			handlers: []slog.Handler{existing.Handler(), otelHandler},
		}))
	}),
)

// Module provides *Providers and wires lifecycle shutdown. Requires
// config.Module + log.Module already present. When opts.Enabled=false the
// module is a no-op (empty Providers, global OTel stays no-op).
var Module = fx.Module("golusoris.otel",
	fx.Provide(loadOptions),
	fx.Provide(func(lc fx.Lifecycle, opts Options, logger *slog.Logger) (*Providers, error) {
		providers, err := New(context.Background(), opts)
		if err != nil {
			return nil, err
		}
		logger.Info("otel: configured",
			slog.Bool("enabled", opts.Enabled),
			slog.String("endpoint", opts.Endpoint),
			slog.String("service", opts.Service.Name),
		)
		lc.Append(fx.Hook{
			OnStop: func(ctx context.Context) error {
				return providers.Shutdown(ctx)
			},
		})
		return providers, nil
	}),
)
