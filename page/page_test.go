package page_test

import (
	"testing"

	"github.com/golusoris/golusoris/page"
)

func TestCursorPage_hasMore(t *testing.T) {
	t.Parallel()
	items := []int{1, 2, 3, 4, 5, 6} // fetched with limit+1=6, limit=5
	p := page.NewCursorPage(items, 5, func(v int) string {
		return string(rune('0' + v))
	})
	if !p.HasMore {
		t.Fatal("expected HasMore=true")
	}
	if len(p.Items) != 5 {
		t.Fatalf("expected 5 items, got %d", len(p.Items))
	}
	if p.NextCursor == "" {
		t.Fatal("expected non-empty NextCursor")
	}
}

func TestCursorPage_lastPage(t *testing.T) {
	t.Parallel()
	items := []int{1, 2, 3}
	p := page.NewCursorPage(items, 5, func(v int) string { return "" })
	if p.HasMore {
		t.Fatal("expected HasMore=false on last page")
	}
	if p.NextCursor != "" {
		t.Fatal("expected empty NextCursor on last page")
	}
}

func TestEncodeDecode(t *testing.T) {
	t.Parallel()
	original := "abc/def=ghi+jkl"
	encoded := page.EncodeCursor(original)
	decoded, err := page.DecodeCursor(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != original {
		t.Fatalf("round-trip mismatch: got %q", decoded)
	}
}

func TestDecodeCursor_invalid(t *testing.T) {
	t.Parallel()
	_, err := page.DecodeCursor("!!!not-base64!!!")
	if err == nil {
		t.Fatal("expected error for invalid cursor")
	}
}

func TestOffsetPage_hasNextPrev(t *testing.T) {
	t.Parallel()
	p := page.NewOffsetPage([]int{1, 2, 3}, 10, 3, 3)
	if !p.HasPrev() {
		t.Fatal("expected HasPrev=true")
	}
	if !p.HasNext() {
		t.Fatal("expected HasNext=true (3+3<10)")
	}

	first := page.NewOffsetPage([]int{1, 2, 3}, 10, 0, 3)
	if first.HasPrev() {
		t.Fatal("first page should not have prev")
	}

	last := page.NewOffsetPage([]int{9, 10}, 10, 8, 3)
	if last.HasNext() {
		t.Fatal("last page should not have next")
	}
}

// FuzzCursorRoundTrip: EncodeCursor then DecodeCursor must be identity
// for any input, and DecodeCursor on arbitrary bytes must never panic.
func FuzzCursorRoundTrip(f *testing.F) {
	seeds := []string{"", "a", "abc/def=ghi+jkl", "\x00\x01\x02", "/"}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		enc := page.EncodeCursor(s)
		dec, err := page.DecodeCursor(enc)
		if err != nil {
			t.Fatalf("round-trip decode failed for %q: %v", s, err)
		}
		if dec != s {
			t.Fatalf("round-trip mismatch: %q -> %q -> %q", s, enc, dec)
		}
		_, _ = page.DecodeCursor(s)
	})
}
