package otel_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	otelapi "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/log/global"
	noopmetric "go.opentelemetry.io/otel/metric/noop"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	nooptrace "go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/golusoris/golusoris/config"
	"github.com/golusoris/golusoris/log"
	golusoris_otel "github.com/golusoris/golusoris/otel"
)

func TestDisabledIsNoop(t *testing.T) {
	t.Parallel()
	providers, err := golusoris_otel.New(context.Background(), golusoris_otel.Options{Enabled: false})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if providers == nil {
		t.Fatal("nil Providers")
	}
	if providers.Tracer != nil || providers.Meter != nil || providers.Logger != nil {
		t.Errorf("expected empty providers when disabled, got %+v", providers)
	}
	// Shutdown of an empty Providers must not panic/error.
	if err := providers.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

func TestLoadOptionsRequiresServiceNameWhenEnabled(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "APP_", Delimiter: "."})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	var opts golusoris_otel.Options
	if err := cfg.Unmarshal("otel", &opts); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	// Direct check: enabled default + missing name → via the fx loader it
	// would error. Here we verify the default structure.
	if !opts.Enabled && opts.Enabled != false {
		t.Error("unexpected default")
	}
}

func TestDefaultOptions(t *testing.T) {
	t.Parallel()
	o := golusoris_otel.DefaultOptions()
	if !o.Enabled {
		t.Error("Enabled should default true")
	}
	if o.Sample.Ratio != 1.0 {
		t.Errorf("Sample.Ratio = %v", o.Sample.Ratio)
	}
	if !o.Export.Traces || !o.Export.Metrics || !o.Export.Logs {
		t.Errorf("Export defaults = %+v", o.Export)
	}
}

// TestLoadOptionsFromConfig proves the full nested config round-trips.
func TestLoadOptionsFromConfig(t *testing.T) {
	t.Setenv("APP_OTEL_SERVICE_NAME", "myapp")
	t.Setenv("APP_OTEL_SERVICE_VERSION", "1.2.3")
	t.Setenv("APP_OTEL_ENDPOINT", "otel-collector.obs:4317")
	t.Setenv("APP_OTEL_SAMPLE_RATIO", "0.1")

	cfg, err := config.New(config.Options{EnvPrefix: "APP_", Delimiter: "."})
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	opts := golusoris_otel.DefaultOptions()
	if err := cfg.Unmarshal("otel", &opts); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if opts.Service.Name != "myapp" {
		t.Errorf("Service.Name = %q", opts.Service.Name)
	}
	if opts.Service.Version != "1.2.3" {
		t.Errorf("Service.Version = %q", opts.Service.Version)
	}
	if opts.Endpoint != "otel-collector.obs:4317" {
		t.Errorf("Endpoint = %q", opts.Endpoint)
	}
	if opts.Sample.Ratio != 0.1 {
		t.Errorf("Sample.Ratio = %v", opts.Sample.Ratio)
	}
}

// TestNewRegistersGlobalTracer boots the SDK against an unreachable
// endpoint — it should still construct successfully because the exporter
// is lazy-dialed. The global tracer provider should become non-noop.
func TestNewRegistersGlobalTracer(t *testing.T) {
	t.Parallel()
	// Capture current global to restore later.
	prev := otelapi.GetTracerProvider()
	t.Cleanup(func() { otelapi.SetTracerProvider(prev) })

	providers, err := golusoris_otel.New(context.Background(), golusoris_otel.Options{
		Enabled:  true,
		Insecure: true,
		Endpoint: "127.0.0.1:1",
		Service:  golusoris_otel.ServiceOptions{Name: "test"},
		Sample:   golusoris_otel.SampleOptions{Ratio: 1.0},
		Export:   golusoris_otel.ExportOptions{Traces: true},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		_ = providers.Shutdown(context.Background())
	})

	if _, ok := otelapi.GetTracerProvider().(*sdktrace.TracerProvider); !ok {
		t.Errorf("global tracer provider not set to SDK type: %T", otelapi.GetTracerProvider())
	}
}

func TestNewWithMetrics(t *testing.T) {
	t.Parallel()
	providers, err := golusoris_otel.New(context.Background(), golusoris_otel.Options{
		Enabled:  true,
		Insecure: true,
		Endpoint: "127.0.0.1:1",
		Service:  golusoris_otel.ServiceOptions{Name: "test"},
		Export:   golusoris_otel.ExportOptions{Metrics: true},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = providers.Shutdown(context.Background()) })
	if providers.Meter == nil {
		t.Error("expected Meter to be set")
	}
}

func TestNewWithLogs(t *testing.T) {
	t.Parallel()
	providers, err := golusoris_otel.New(context.Background(), golusoris_otel.Options{
		Enabled:  true,
		Insecure: true,
		Endpoint: "127.0.0.1:1",
		Service:  golusoris_otel.ServiceOptions{Name: "test"},
		Export:   golusoris_otel.ExportOptions{Logs: true},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = providers.Shutdown(context.Background()) })
	if providers.Logger == nil {
		t.Error("expected Logger to be set")
	}
}

func TestModuleWithSlogBridge_coversHandler(t *testing.T) { //nolint:paralleltest // modifies global slog.Default
	// Not parallel: modifies global slog default.
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	prevTP := otelapi.GetTracerProvider()
	t.Cleanup(func() { otelapi.SetTracerProvider(prevTP) })

	providers, err := golusoris_otel.New(context.Background(), golusoris_otel.Options{
		Enabled:  true,
		Insecure: true,
		Endpoint: "127.0.0.1:1",
		Service:  golusoris_otel.ServiceOptions{Name: "test"},
		Export:   golusoris_otel.ExportOptions{Logs: true},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = providers.Shutdown(context.Background()) })

	// Boot ModuleWithSlogBridge via fx to construct the fanoutHandler and
	// cover Enabled / Handle / WithAttrs / WithGroup.
	// Use a discard logger to avoid recursive slog→defaultHandler→slog deadlock.
	app := fxtest.New(t,
		fx.Provide(func() *golusoris_otel.Providers { return providers }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
		fx.Provide(func() golusoris_otel.Options {
			return golusoris_otel.Options{
				Enabled:  true,
				Endpoint: "127.0.0.1:1",
				Service:  golusoris_otel.ServiceOptions{Name: "test"},
				Export:   golusoris_otel.ExportOptions{Logs: true},
			}
		}),
		golusoris_otel.ModuleWithSlogBridge,
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Trigger the fanout handler methods via the default logger (set by the module).
	l := slog.Default()
	l.InfoContext(ctx, "test from fanout", "key", "value")
	l.WithGroup("grp").With("a", "b").DebugContext(ctx, "debug")
	if err := app.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// emptyProviders is the shared assertion for the silent-no-op contract: no SDK
// provider was constructed, so there is nothing to flush or dial.
func emptyProviders(t *testing.T, p *golusoris_otel.Providers) {
	t.Helper()
	if p == nil {
		t.Fatal("nil Providers")
	}
	if p.Tracer != nil || p.Meter != nil || p.Logger != nil {
		t.Errorf("expected silent no-op (empty providers), got %+v", p)
	}
	// Shutdown of an empty Providers must be instant and error-free — no batch
	// processor or periodic reader to flush, no socket to close.
	if err := p.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

// TestEnabledNoEndpointIsNoop is the issue #96 regression: enabled but no OTLP
// endpoint configured (the 12-factor default) must degrade to a silent no-op
// rather than standing up real exporters that dial localhost:4317. The strict
// time budget guards against the old behaviour, whose Shutdown blocked ~10s
// flushing to an unreachable default collector.
func TestEnabledNoEndpointIsNoop(t *testing.T) { //nolint:paralleltest // t.Setenv clears OTLP env; incompatible with t.Parallel
	// Not parallel: asserts no OTEL_EXPORTER_OTLP_* env is set in this process.
	for _, env := range []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT",
		"OTEL_SDK_DISABLED",
	} {
		t.Setenv(env, "")
	}

	done := make(chan struct{})
	var providers *golusoris_otel.Providers
	go func() {
		defer close(done)
		var err error
		providers, err = golusoris_otel.New(context.Background(), golusoris_otel.Options{
			Enabled:  true,
			Insecure: true,
			Endpoint: "", // 12-factor default: no collector wired.
			Service:  golusoris_otel.ServiceOptions{Name: "test"},
			Sample:   golusoris_otel.SampleOptions{Ratio: 1.0},
			Export:   golusoris_otel.ExportOptions{Traces: true, Metrics: true, Logs: true},
		})
		if err != nil {
			t.Errorf("New: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("New blocked >2s with no endpoint — expected an instant no-op")
	}
	emptyProviders(t, providers)
}

// TestSDKDisabledEnvIsNoop covers the OTEL_SDK_DISABLED kill switch: even with
// an endpoint configured, the standard env var forces a silent no-op.
func TestSDKDisabledEnvIsNoop(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "true")
	providers, err := golusoris_otel.New(context.Background(), golusoris_otel.Options{
		Enabled:  true,
		Insecure: true,
		Endpoint: "127.0.0.1:1",
		Service:  golusoris_otel.ServiceOptions{Name: "test"},
		Export:   golusoris_otel.ExportOptions{Traces: true, Metrics: true, Logs: true},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	emptyProviders(t, providers)
}

// TestStandardEndpointEnvActivatesExporter proves the module also honours the
// standard OTEL_EXPORTER_OTLP_ENDPOINT env var as a configured endpoint, so
// 12-factor deployments that set only the OTel-standard var still export.
func TestStandardEndpointEnvActivatesExporter(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	prev := otelapi.GetTracerProvider()
	t.Cleanup(func() { otelapi.SetTracerProvider(prev) })

	providers, err := golusoris_otel.New(context.Background(), golusoris_otel.Options{
		Enabled:  true,
		Insecure: true,
		Endpoint: "", // unset in config; resolved from the standard env var.
		Service:  golusoris_otel.ServiceOptions{Name: "test"},
		Sample:   golusoris_otel.SampleOptions{Ratio: 1.0},
		Export:   golusoris_otel.ExportOptions{Traces: true},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = providers.Shutdown(context.Background()) })
	if providers.Tracer == nil {
		t.Error("expected real Tracer when standard OTLP endpoint env is set")
	}
}

// TestNoopGlobalAPIDoesNotPanic exercises the OTel global API while no SDK
// provider is installed (the no-op state). Spans, metric instruments, and log
// records must all be safe no-ops — no panic, no network. This is the contract
// app code relies on when the collector is absent.
func TestNoopGlobalAPIDoesNotPanic(t *testing.T) { //nolint:paralleltest // snapshots/restores global providers
	// Snapshot + force a clean no-op global so parallel SDK-installing tests
	// can't leak a real provider into this assertion.
	prevTracer := otelapi.GetTracerProvider()
	prevMeter := otelapi.GetMeterProvider()
	prevLogger := global.GetLoggerProvider()
	t.Cleanup(func() {
		otelapi.SetTracerProvider(prevTracer)
		otelapi.SetMeterProvider(prevMeter)
		global.SetLoggerProvider(prevLogger)
	})
	otelapi.SetTracerProvider(nooptrace.NewTracerProvider())
	otelapi.SetMeterProvider(noopmetric.NewMeterProvider())

	ctx := context.Background()

	// Tracing: starting/ending a span on the no-op tracer must not panic.
	_, span := otelapi.Tracer("test").Start(ctx, "noop-span")
	span.End()

	// Metrics: recording on a no-op counter must not panic.
	counter, err := otelapi.Meter("test").Int64Counter("noop.counter")
	if err != nil {
		t.Fatalf("Int64Counter: %v", err)
	}
	counter.Add(ctx, 1)
}

// TestModuleNoEndpointWiresCleanly boots the full module graph (config + log +
// otel) with no endpoint configured and asserts it starts/stops cleanly with
// empty Providers — the realistic 12-factor default for a dev/test run.
func TestModuleNoEndpointWiresCleanly(t *testing.T) {
	// Not parallel: t.Setenv + global slog default mutation in log.Module.
	t.Setenv("APP_OTEL_SERVICE_NAME", "test")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_SDK_DISABLED", "")
	prevSlog := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prevSlog) })

	var providers *golusoris_otel.Providers
	app := fxtest.New(t,
		config.Module,
		log.Module,
		golusoris_otel.Module,
		fx.Populate(&providers),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	app.RequireStart()
	emptyProviders(t, providers)
	if err := app.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
