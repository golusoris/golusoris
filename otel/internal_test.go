package otel

import (
	"testing"

	"github.com/golusoris/golusoris/config"
)

func TestDefaultOptions_enabled(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	if !opts.Enabled {
		t.Error("Enabled = false, want true")
	}
}

func TestDefaultOptions_sampleRatio(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	if opts.Sample.Ratio != 1.0 {
		t.Errorf("Sample.Ratio = %v, want 1.0", opts.Sample.Ratio)
	}
}

func TestDefaultOptions_insecure(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	if !opts.Insecure {
		t.Error("Insecure = false, want true")
	}
}

func TestDefaultOptions_exportToggles(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	if !opts.Export.Traces {
		t.Error("Export.Traces = false, want true")
	}
	if !opts.Export.Metrics {
		t.Error("Export.Metrics = false, want true")
	}
	if !opts.Export.Logs {
		t.Error("Export.Logs = false, want true")
	}
}

func TestLoadOptions_requiresServiceName(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_OTEL_"})
	if err != nil {
		t.Fatal(err)
	}
	// Default opts have Enabled=true but no service name — expect an error.
	_, err = loadOptions(cfg)
	if err == nil {
		t.Error("loadOptions() with enabled=true and no service name should return error")
	}
}

func TestLoadOptions_disabledSkipsServiceNameCheck(t *testing.T) {
	// t.Setenv is incompatible with t.Parallel()
	t.Setenv("TEST_OTEL2_OTEL_ENABLED", "false")
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_OTEL2_"})
	if err != nil {
		t.Fatal(err)
	}
	opts, err := loadOptions(cfg)
	if err != nil {
		t.Fatalf("loadOptions() with enabled=false should not require service name: %v", err)
	}
	if opts.Enabled {
		t.Error("Enabled = true, want false")
	}
}
