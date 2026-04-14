package redis

import (
	"testing"

	"github.com/golusoris/golusoris/config"
)

func TestDefaultOptions_addr(t *testing.T) {
	t.Parallel()
	opts := defaultOptions()
	if opts.Addr != "localhost:6379" {
		t.Errorf("Addr = %q, want \"localhost:6379\"", opts.Addr)
	}
}

func TestDefaultOptions_zeroValues(t *testing.T) {
	t.Parallel()
	opts := defaultOptions()
	if opts.Username != "" {
		t.Errorf("Username = %q, want \"\"", opts.Username)
	}
	if opts.Password != "" {
		t.Errorf("Password = %q, want \"\"", opts.Password)
	}
	if opts.DB != 0 {
		t.Errorf("DB = %d, want 0", opts.DB)
	}
	if opts.TLS {
		t.Error("TLS = true, want false")
	}
}

func TestLoadOptions_defaults(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_REDIS_"})
	if err != nil {
		t.Fatal(err)
	}
	opts, err := loadOptions(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Addr != "localhost:6379" {
		t.Errorf("Addr = %q, want \"localhost:6379\"", opts.Addr)
	}
}
