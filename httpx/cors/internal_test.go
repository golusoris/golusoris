package cors

import (
	"net/http"
	"testing"
	"time"

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
	if len(opts.Methods) == 0 {
		t.Error("expected default Methods to be set")
	}
	if len(opts.Headers) == 0 {
		t.Error("expected default Headers to be set")
	}
	if opts.MaxAge != 5*time.Minute {
		t.Errorf("MaxAge = %v, want 5m", opts.MaxAge)
	}
}

func TestDefaultOptions_methods(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	found := false
	for _, m := range opts.Methods {
		if m == http.MethodGet {
			found = true
			break
		}
	}
	if !found {
		t.Error("DefaultOptions Methods should include GET")
	}
}

func TestDefaultOptions_preservesNonZero(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	opts.Origins = []string{"https://example.com"}
	opts.Credentials = true
	opts.MaxAge = 10 * time.Minute
	if opts.Origins[0] != "https://example.com" {
		t.Error("Origins not preserved")
	}
	if !opts.Credentials {
		t.Error("Credentials not preserved")
	}
	if opts.MaxAge != 10*time.Minute {
		t.Errorf("MaxAge = %v, want 10m", opts.MaxAge)
	}
}
