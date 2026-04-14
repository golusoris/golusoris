package markdown_test

import (
	"strings"
	"testing"

	"github.com/golusoris/golusoris/markdown"
)

func TestRender_heading(t *testing.T) {
	t.Parallel()
	out, err := markdown.Render([]byte("# Hello"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "<h1") {
		t.Fatalf("expected h1 tag, got: %s", out)
	}
}

func TestRender_table(t *testing.T) {
	t.Parallel()
	src := "| A | B |\n|---|---|\n| 1 | 2 |"
	out, err := markdown.Render([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "<table>") {
		t.Fatalf("expected table tag, got: %s", out)
	}
}

func TestRender_strikethrough(t *testing.T) {
	t.Parallel()
	out, err := markdown.Render([]byte("~~strike~~"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "<del>") {
		t.Fatalf("expected del tag, got: %s", out)
	}
}

func TestRenderString(t *testing.T) {
	t.Parallel()
	got := markdown.RenderString("**bold**")
	if !strings.Contains(got, "<strong>") {
		t.Fatalf("expected strong tag, got: %s", got)
	}
}
