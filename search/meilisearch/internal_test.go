package meilisearch

import (
	"strings"
	"testing"
)

func TestFiltersToMeili_string(t *testing.T) {
	t.Parallel()
	got := filtersToMeili(map[string]any{"name": "alice"})
	if !strings.Contains(got, "name") {
		t.Errorf("filtersToMeili = %q, want it to contain 'name'", got)
	}
}

func TestFiltersToMeili_bool(t *testing.T) {
	t.Parallel()
	got := filtersToMeili(map[string]any{"active": true})
	if !strings.Contains(got, "true") {
		t.Errorf("filtersToMeili = %q, want it to contain 'true'", got)
	}
}

func TestFiltersToMeili_int(t *testing.T) {
	t.Parallel()
	got := filtersToMeili(map[string]any{"age": 30})
	if !strings.Contains(got, "30") {
		t.Errorf("filtersToMeili = %q, want it to contain '30'", got)
	}
}

func TestFiltersToMeili_int64(t *testing.T) {
	t.Parallel()
	got := filtersToMeili(map[string]any{"score": int64(99)})
	if !strings.Contains(got, "99") {
		t.Errorf("filtersToMeili = %q, want it to contain '99'", got)
	}
}

func TestFiltersToMeili_float64(t *testing.T) {
	t.Parallel()
	got := filtersToMeili(map[string]any{"ratio": float64(1.5)})
	if !strings.Contains(got, "1.5") {
		t.Errorf("filtersToMeili = %q, want it to contain '1.5'", got)
	}
}

func TestFiltersToMeili_default(t *testing.T) {
	t.Parallel()
	// Fallback %v branch.
	got := filtersToMeili(map[string]any{"tag": []string{"a", "b"}})
	if !strings.Contains(got, "tag") {
		t.Errorf("filtersToMeili = %q, want it to contain 'tag'", got)
	}
}

func TestFiltersToMeili_empty(t *testing.T) {
	t.Parallel()
	if got := filtersToMeili(nil); got != "" {
		t.Errorf("filtersToMeili(nil) = %q, want empty", got)
	}
}

func TestFiltersToMeili_multipleAND(t *testing.T) {
	t.Parallel()
	got := filtersToMeili(map[string]any{"a": "x", "b": "y"})
	if !strings.Contains(got, "AND") {
		t.Errorf("filtersToMeili with 2 keys = %q, want 'AND'", got)
	}
}
