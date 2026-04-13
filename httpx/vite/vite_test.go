package vite_test

import (
	"testing"
	"testing/fstest"

	"github.com/golusoris/golusoris/httpx/vite"
)

const manifestJSON = `{
  "src/main.tsx": {
    "file": "assets/main-abc123.js",
    "src": "src/main.tsx",
    "isEntry": true,
    "css": ["assets/main-xyz.css"],
    "imports": ["src/shared.ts"]
  },
  "src/shared.ts": {
    "file": "assets/shared-def.js",
    "css": ["assets/shared-uvw.css"]
  }
}`

func TestNewFromBytes(t *testing.T) {
	t.Parallel()
	m, err := vite.NewFromBytes([]byte(manifestJSON))
	if err != nil {
		t.Fatalf("NewFromBytes: %v", err)
	}
	if got := m.File("src/main.tsx"); got != "assets/main-abc123.js" {
		t.Errorf("File = %q", got)
	}
}

func TestCSSFollowsImports(t *testing.T) {
	t.Parallel()
	m, err := vite.NewFromBytes([]byte(manifestJSON))
	if err != nil {
		t.Fatalf("NewFromBytes: %v", err)
	}
	css := m.CSS("src/main.tsx")
	want := map[string]bool{"assets/main-xyz.css": true, "assets/shared-uvw.css": true}
	if len(css) != 2 {
		t.Fatalf("CSS = %v, want 2 entries", css)
	}
	for _, c := range css {
		if !want[c] {
			t.Errorf("unexpected CSS %q", c)
		}
	}
}

func TestNewFromFS(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"manifest.json": {Data: []byte(manifestJSON)},
	}
	m, err := vite.NewFromFS(fsys, "manifest.json")
	if err != nil {
		t.Fatalf("NewFromFS: %v", err)
	}
	if got := m.File("src/main.tsx"); got != "assets/main-abc123.js" {
		t.Errorf("File = %q", got)
	}
}

func TestMissingEntryReturnsEmpty(t *testing.T) {
	t.Parallel()
	m, _ := vite.NewFromBytes([]byte(manifestJSON))
	if got := m.File("does/not/exist"); got != "" {
		t.Errorf("File = %q, want empty", got)
	}
}
