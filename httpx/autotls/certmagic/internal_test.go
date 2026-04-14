package certmagic

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
	if len(opts.Domains) != 0 {
		t.Errorf("Domains = %v, want empty", opts.Domains)
	}
	if opts.Staging {
		t.Error("Staging should be false by default")
	}
	if opts.Email != "" {
		t.Errorf("Email = %q, want empty", opts.Email)
	}
}

func TestOptions_preservesNonZero(t *testing.T) {
	t.Parallel()
	opts := Options{
		Domains: []string{"example.com"},
		Email:   "admin@example.com",
		Staging: true,
	}
	if opts.Domains[0] != "example.com" {
		t.Error("Domains not preserved")
	}
	if opts.Email != "admin@example.com" {
		t.Error("Email not preserved")
	}
	if !opts.Staging {
		t.Error("Staging not preserved")
	}
}
