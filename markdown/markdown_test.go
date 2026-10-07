// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package markdown_test

import (
	"bytes"
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
	got, err := markdown.RenderString("**bold**")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "<strong>") {
		t.Fatalf("expected strong tag, got: %s", got)
	}
}

func TestRenderTo(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := markdown.RenderTo(&buf, []byte("# Title")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "<h1") {
		t.Fatalf("expected h1 tag, got: %s", buf.String())
	}
}

func TestRenderTo_nilBufferReturnsError(t *testing.T) {
	t.Parallel()
	if err := markdown.RenderTo(nil, []byte("# Title")); err == nil {
		t.Fatal("expected nil buffer error")
	}
}

func TestRender_extensions(t *testing.T) {
	t.Parallel()
	src := "# Hello World\n\nline one\nline two\n\n- [x] done\n\n[^1]\n\n[^1]: note\n\n\"quoted\""
	out, err := markdown.RenderString(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<h1 id="hello-world">`,
		"line one<br />",
		`type="checkbox"`,
		`class="footnotes"`,
		"“quoted”",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in extension output: %s", want, out)
		}
	}
}

func TestRender_doesNotEnableUnsafeHTML(t *testing.T) {
	t.Parallel()
	out, err := markdown.RenderString(`<script>alert("x")</script>`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "<script>") {
		t.Fatalf("unsafe HTML rendered: %s", out)
	}
}
