package geofence

import (
	"testing"

	"github.com/golusoris/golusoris/config"
)

func TestLoadOptions_emptyReturnsZero(t *testing.T) {
	t.Parallel()
	cfg, err := config.New(config.Options{EnvPrefix: "TEST_GEOFENCE_"})
	if err != nil {
		t.Fatal(err)
	}
	opts, err := loadOptions(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if opts.MmdbPath != "" {
		t.Errorf("MmdbPath = %q, want \"\"", opts.MmdbPath)
	}
	if len(opts.Allow) != 0 {
		t.Errorf("Allow = %v, want empty", opts.Allow)
	}
	if len(opts.Deny) != 0 {
		t.Errorf("Deny = %v, want empty", opts.Deny)
	}
}

func TestNew_noOptionsReturnsNonNilMiddleware(t *testing.T) {
	t.Parallel()
	mw, reader, err := New(Options{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if mw == nil {
		t.Error("New() middleware = nil, want non-nil identity")
	}
	if reader != nil {
		t.Errorf("New() reader = %v, want nil", reader)
	}
}

func TestNew_policyWithoutMmdbIsError(t *testing.T) {
	t.Parallel()
	_, _, err := New(Options{Allow: []string{"DE"}})
	if err == nil {
		t.Error("New() with Allow set but no MmdbPath should return error")
	}
}
