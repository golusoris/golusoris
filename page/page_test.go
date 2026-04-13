package page_test

import (
	"testing"

	"github.com/golusoris/golusoris/page"
)

func TestCursorPage_hasMore(t *testing.T) {
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
	_, err := page.DecodeCursor("!!!not-base64!!!")
	if err == nil {
		t.Fatal("expected error for invalid cursor")
	}
}

func TestOffsetPage_hasNextPrev(t *testing.T) {
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
