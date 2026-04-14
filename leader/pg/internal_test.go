package pg

import (
	"testing"
	"time"
)

func TestDefaultOptions(t *testing.T) {
	t.Parallel()
	opts := DefaultOptions()
	if opts.Enabled {
		t.Fatal("want Enabled=false")
	}
	if opts.PG.Retry != 2*time.Second {
		t.Fatalf("want PG.Retry=2s, got %v", opts.PG.Retry)
	}
}

func TestWithDefaults_preserves(t *testing.T) {
	t.Parallel()
	opts := Options{
		Enabled:  true,
		Name:     "myapp",
		Identity: "pod-1",
		PG:       BackendOptions{Retry: 5 * time.Second},
	}
	got := opts.withDefaults()
	if got.PG.Retry != 5*time.Second {
		t.Fatalf("want Retry preserved as 5s, got %v", got.PG.Retry)
	}
	if got.Name != "myapp" {
		t.Fatalf("want Name preserved as %q, got %q", "myapp", got.Name)
	}
}

func TestKeyFor_stable(t *testing.T) {
	t.Parallel()
	a, b := keyFor("foo"), keyFor("foo")
	if a != b {
		t.Fatalf("want keyFor to be deterministic; got %d and %d", a, b)
	}
}

func TestKeyFor_different(t *testing.T) {
	t.Parallel()
	if keyFor("foo") == keyFor("bar") {
		t.Fatal("want different keys for different names")
	}
}
