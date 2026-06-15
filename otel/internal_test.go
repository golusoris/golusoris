package otel

import (
	"path/filepath"
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

func TestLoadOptions_defaultsServiceName(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_OTEL_"})
	if err != nil {
		t.Fatal(err)
	}
	// Default opts have Enabled=true but no configured service name; loadOptions
	// must derive one from build info / argv instead of erroring (issue #254).
	opts, err := loadOptions(cfg)
	if err != nil {
		t.Fatalf("loadOptions() should derive a service name, got error: %v", err)
	}
	if opts.Service.Name == "" {
		t.Error("Service.Name = \"\", want a derived name")
	}
	// Under `go test`, the build-info path is "<module>/otel.test", so the
	// derived name is "otel" (last segment, extension stripped).
	if opts.Service.Name != "otel" {
		t.Errorf("Service.Name = %q, want %q", opts.Service.Name, "otel")
	}
}

func TestLoadOptions_explicitServiceNameOverridesDerived(t *testing.T) {
	// t.Setenv is incompatible with t.Parallel().
	t.Setenv("TEST_OTEL3_OTEL_SERVICE_NAME", "myapp")
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_OTEL3_"})
	if err != nil {
		t.Fatal(err)
	}
	opts, err := loadOptions(cfg)
	if err != nil {
		t.Fatalf("loadOptions(): %v", err)
	}
	if opts.Service.Name != "myapp" {
		t.Errorf("Service.Name = %q, want %q (explicit config must win)", opts.Service.Name, "myapp")
	}
}

func TestDefaultServiceName_derivesFromBuildInfo(t *testing.T) {
	t.Parallel()
	// The test binary's build-info path ends in "otel.test", so the derived
	// name strips the ".test" extension down to "otel".
	if got := defaultServiceName(); got != "otel" {
		t.Errorf("defaultServiceName() = %q, want %q", got, "otel")
	}
}

func TestCleanServiceName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "server", "server"},
		{"strips exe extension", "server.exe", "server"},
		{"strips test extension", "otel.test", "otel"},
		{"dot sentinel", ".", ""},
		{"separator sentinel", string(filepath.Separator), ""},
		{"empty", "", ""},
		{"keeps dotted name", "my.svc", "my"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := cleanServiceName(tc.in); got != tc.want {
				t.Errorf("cleanServiceName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
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
