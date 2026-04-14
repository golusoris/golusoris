package memory

import (
	"testing"
	"time"

	"github.com/golusoris/golusoris/config"
)

func TestDefaultOptions_maxSize(t *testing.T) {
	t.Parallel()
	opts := defaultOptions()
	if opts.MaxSize != 10_000 {
		t.Errorf("MaxSize = %d, want 10000", opts.MaxSize)
	}
}

func TestDefaultOptions_ttl(t *testing.T) {
	t.Parallel()
	opts := defaultOptions()
	if opts.TTL != 5*time.Minute {
		t.Errorf("TTL = %v, want 5m", opts.TTL)
	}
}

func TestLoadOptions_defaults(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_MEMORY_"})
	if err != nil {
		t.Fatal(err)
	}
	opts, err := loadOptions(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if opts.MaxSize != 10_000 {
		t.Errorf("MaxSize = %d, want 10000", opts.MaxSize)
	}
	if opts.TTL != 5*time.Minute {
		t.Errorf("TTL = %v, want 5m", opts.TTL)
	}
}
