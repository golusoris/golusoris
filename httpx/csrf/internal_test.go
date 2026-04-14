package csrf

import (
	"testing"

	"github.com/golusoris/golusoris/config"
)

func TestLoadOptions_defaults(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_"})
	if err != nil {
		t.Fatal(err)
	}
	opts, err := loadOptions(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !opts.Secure {
		t.Error("expected Secure=true by default")
	}
	if opts.Path != "/" {
		t.Errorf("Path = %q, want /", opts.Path)
	}
	if opts.Secret != "" {
		t.Error("expected empty Secret by default")
	}
}

func TestDefaultOptions_values(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	if !opts.Secure {
		t.Error("Secure should be true")
	}
	if opts.Path != "/" {
		t.Errorf("Path = %q, want /", opts.Path)
	}
}

func TestDefaultOptions_preservesNonZero(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	opts.Domain = "example.com"
	opts.Secure = false
	opts.Path = "/api"
	if opts.Domain != "example.com" {
		t.Error("Domain not preserved")
	}
	if opts.Secure {
		t.Error("Secure should remain false after explicit set")
	}
	if opts.Path != "/api" {
		t.Errorf("Path = %q, want /api", opts.Path)
	}
}
