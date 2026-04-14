package pgfts

import (
	"testing"

	"github.com/golusoris/golusoris/search"
)

func TestIsSafeIdent_valid(t *testing.T) {
	t.Parallel()
	cases := []string{"docs", "search_vec", "my_table", "public.docs", "Col1"}
	for _, c := range cases {
		if !isSafeIdent(c) {
			t.Errorf("isSafeIdent(%q) = false, want true", c)
		}
	}
}

func TestIsSafeIdent_invalid(t *testing.T) {
	t.Parallel()
	cases := []string{"", "bad name", "drop;table", "a-b", "x'y", "a b"}
	for _, c := range cases {
		if isSafeIdent(c) {
			t.Errorf("isSafeIdent(%q) = true, want false", c)
		}
	}
}

func TestToFloat64(t *testing.T) {
	t.Parallel()
	if got := toFloat64(float64(3.14)); got != 3.14 {
		t.Errorf("toFloat64(float64(3.14)) = %v, want 3.14", got)
	}
	if got := toFloat64(float32(1.5)); got != float64(float32(1.5)) {
		t.Errorf("toFloat64(float32(1.5)) = %v", got)
	}
	if got := toFloat64("not a number"); got != 0 {
		t.Errorf("toFloat64(string) = %v, want 0", got)
	}
}

func TestBuildSQL_basic(t *testing.T) {
	t.Parallel()
	s := New(nil, Options{Table: "docs", VectorColumn: "search_vec"})
	sql, err := s.buildSQL("docs", search.Query{Q: "test"})
	if err != nil {
		t.Fatalf("buildSQL: %v", err)
	}
	if sql == "" {
		t.Fatal("expected non-empty SQL")
	}
}

func TestBuildSQL_unsafeTable(t *testing.T) {
	t.Parallel()
	s := New(nil, Options{VectorColumn: "search_vec"})
	_, err := s.buildSQL("bad name", search.Query{Q: "test"})
	if err == nil {
		t.Fatal("expected error for unsafe table name")
	}
}
