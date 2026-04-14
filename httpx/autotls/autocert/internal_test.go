package autocert

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
	if opts.Cache != "./certs" {
		t.Errorf("Cache = %q, want ./certs", opts.Cache)
	}
	if len(opts.Domains) != 0 {
		t.Errorf("Domains = %v, want empty", opts.Domains)
	}
}

func TestDefaultOptions_values(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	if opts.Cache != "./certs" {
		t.Errorf("Cache = %q, want ./certs", opts.Cache)
	}
	if opts.Email != "" {
		t.Errorf("Email = %q, want empty", opts.Email)
	}
}

func TestDefaultOptions_preservesNonZero(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	opts.Cache = "/var/certs"
	opts.Email = "admin@example.com"
	opts.Domains = []string{"example.com"}
	if opts.Cache != "/var/certs" {
		t.Error("Cache not preserved")
	}
	if opts.Email != "admin@example.com" {
		t.Error("Email not preserved")
	}
	if len(opts.Domains) != 1 || opts.Domains[0] != "example.com" {
		t.Error("Domains not preserved")
	}
}
