package twotier

import (
	"testing"
	"time"

	"github.com/golusoris/golusoris/config"
)

func TestLoadOptions_defaults(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_TWOTIER_"})
	if err != nil {
		t.Fatal(err)
	}
	opts, err := loadOptions(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if opts.L1TTL != time.Minute {
		t.Errorf("L1TTL = %v, want 1m", opts.L1TTL)
	}
	if opts.L2TTL != 5*time.Minute {
		t.Errorf("L2TTL = %v, want 5m", opts.L2TTL)
	}
}
