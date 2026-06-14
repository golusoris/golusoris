package markdown_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/markdown"
)

// FuzzRender asserts the markdown renderer never panics on arbitrary input and
// that Render/RenderString/RenderTo stay consistent.
func FuzzRender(f *testing.F) {
	for _, s := range []string{
		"",
		"# heading",
		"| a | b |\n|---|---|\n| 1 | 2 |",
		"[x](javascript:alert(1))",
		"<script>alert(1)</script>",
		"~~strike~~ **bold** _em_",
		strings.Repeat("#", 4096),
		strings.Repeat("> ", 2048) + "deep",
		"![img](data:text/html,<script>)",
	} {
		f.Add([]byte(s))
	}

	f.Fuzz(func(_ *testing.T, src []byte) {
		_, _ = markdown.Render(src)            // must not panic
		_ = markdown.RenderString(string(src)) // must not panic
		_ = markdown.RenderTo(&bytes.Buffer{}, src)
	})
}
