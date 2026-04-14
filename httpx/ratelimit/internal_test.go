package ratelimit

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
	// Default is empty rate (no-op middleware).
	if opts.Rate != "" {
		t.Errorf("Rate = %q, want empty", opts.Rate)
	}
	if opts.TrustXFF {
		t.Error("TrustXFF should be false by default")
	}
}

func TestDefaultOptions_empty(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	if opts.Rate != "" {
		t.Errorf("Rate = %q, want empty", opts.Rate)
	}
	if opts.TrustXFF {
		t.Error("TrustXFF should be false")
	}
}

func TestDefaultOptions_preservesNonZero(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	opts.Rate = "100-M"
	opts.TrustXFF = true
	if opts.Rate != "100-M" {
		t.Errorf("Rate = %q, want 100-M", opts.Rate)
	}
	if !opts.TrustXFF {
		t.Error("TrustXFF not preserved")
	}
}
