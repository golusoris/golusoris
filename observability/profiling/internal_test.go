package profiling

import (
	"testing"

	"github.com/golusoris/golusoris/config"
)

func TestLoadOptions_defaults(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "APP_"})
	if err != nil {
		t.Fatal(err)
	}
	opts, err := loadOptions(cfg)
	if err != nil {
		t.Fatalf("loadOptions: %v", err)
	}
	if opts.Server != "http://pyroscope:4040" {
		t.Errorf("Server = %q", opts.Server)
	}
}
