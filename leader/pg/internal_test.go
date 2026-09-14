// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

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
	a, err := keyFor("foo")
	if err != nil {
		t.Fatalf("keyFor: %v", err)
	}
	b, err := keyFor("foo")
	if err != nil {
		t.Fatalf("keyFor: %v", err)
	}
	if a != b {
		t.Fatalf("want keyFor to be deterministic; got %d and %d", a, b)
	}
}

func TestKeyFor_different(t *testing.T) {
	t.Parallel()
	foo, err := keyFor("foo")
	if err != nil {
		t.Fatalf("keyFor: %v", err)
	}
	bar, err := keyFor("bar")
	if err != nil {
		t.Fatalf("keyFor: %v", err)
	}
	if foo == bar {
		t.Fatal("want different keys for different names")
	}
}
