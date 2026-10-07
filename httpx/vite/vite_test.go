// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package vite_test

import (
	"io/fs"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/golusoris/golusoris/httpx/vite"
)

type typedNilFS struct{}

func (*typedNilFS) Open(string) (fs.File, error) { panic("typed-nil filesystem used") }

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

func TestEntryReturnsSliceSnapshot(t *testing.T) {
	t.Parallel()
	m, err := vite.NewFromBytes([]byte(manifestJSON))
	if err != nil {
		t.Fatalf("NewFromBytes: %v", err)
	}
	entry, ok := m.Entry("src/main.tsx")
	if !ok {
		t.Fatal("Entry: missing main entry")
	}
	entry.CSS[0] = "mutated.css"
	entry.Imports[0] = "mutated.ts"

	fresh, ok := m.Entry("src/main.tsx")
	if !ok {
		t.Fatal("Entry: missing main entry after mutation")
	}
	if fresh.CSS[0] != "assets/main-xyz.css" || fresh.Imports[0] != "src/shared.ts" {
		t.Fatalf("Entry aliases manifest state: %+v", fresh)
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

func TestCSSDeduplicatesSharedAssetsInFirstSeenOrder(t *testing.T) {
	t.Parallel()
	const sharedManifest = `{
  "src/main.ts": {"imports":["src/a.ts","src/b.ts"],"css":["main.css","shared.css"]},
  "src/a.ts": {"css":["a.css","shared.css"]},
  "src/b.ts": {"css":["shared.css","b.css"]}
}`
	m, err := vite.NewFromBytes([]byte(sharedManifest))
	if err != nil {
		t.Fatalf("NewFromBytes: %v", err)
	}
	want := []string{"main.css", "shared.css", "a.css", "b.css"}
	got := m.CSS("src/main.ts")
	if !slices.Equal(got, want) {
		t.Fatalf("CSS = %v, want %v", got, want)
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

func TestNewFromFSTypedNilReturnsError(t *testing.T) {
	t.Parallel()
	var filesystem *typedNilFS
	if _, err := vite.NewFromFS(filesystem, "manifest.json"); err == nil {
		t.Fatal("typed-nil filesystem should return an error")
	}
}

func TestMissingEntryReturnsEmpty(t *testing.T) {
	t.Parallel()
	m, _ := vite.NewFromBytes([]byte(manifestJSON))
	if got := m.File("does/not/exist"); got != "" {
		t.Errorf("File = %q, want empty", got)
	}
}
