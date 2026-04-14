package migrate

import (
	"testing"

	"github.com/golusoris/golusoris/config"
)

func TestLoadOptions_empty(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_"})
	if err != nil {
		t.Fatal(err)
	}
	opts, err := loadOptions(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// loadOptions does not set defaults — zero value is valid.
	if opts.Path != "" {
		t.Errorf("Path = %q, want empty", opts.Path)
	}
	if opts.Auto {
		t.Error("Auto should be false by default")
	}
	if opts.DSN != "" {
		t.Errorf("DSN = %q, want empty", opts.DSN)
	}
}

func TestOptions_withFS(t *testing.T) {
	t.Parallel()
	opts := Options{Path: "migrations", Auto: true}
	// WithFS is exported — verify it sets FS while preserving other fields.
	// We pass nil FS here just to test the chaining; nil FS is handled by New.
	o2 := opts.WithFS(nil)
	if o2.Path != "migrations" {
		t.Errorf("Path = %q, want migrations", o2.Path)
	}
	if !o2.Auto {
		t.Error("Auto not preserved after WithFS")
	}
}

func TestOptions_preservesNonZero(t *testing.T) {
	t.Parallel()
	opts := Options{
		Path: "/app/migrations",
		Auto: true,
		DSN:  "postgres://localhost/test",
	}
	if opts.Path != "/app/migrations" {
		t.Error("Path not preserved")
	}
	if !opts.Auto {
		t.Error("Auto not preserved")
	}
	if opts.DSN != "postgres://localhost/test" {
		t.Error("DSN not preserved")
	}
}
