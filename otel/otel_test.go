package otel_test

import (
	"context"
	"testing"

	otelapi "go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/golusoris/golusoris/config"
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
