// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package golusoris_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/core/astx"
	"github.com/golusoris/golusoris/core/capabilities"
)

// TestCapabilitiesContract is the drift guard between capabilities.yaml and
// the tree: every listed import must be a real package directory, and every
// importable package directory must be listed. Praetor and other governance
// tooling trust this file, so it cannot silently rot.
func TestCapabilitiesContract(t *testing.T) {
	t.Parallel()
	idx, err := capabilities.Load(capabilities.FileName)
	if err != nil {
		t.Fatalf("load %s: %v", capabilities.FileName, err)
	}
	if idx.Framework != "github.com/golusoris/golusoris" {
		t.Fatalf("framework = %q", idx.Framework)
	}

	// 1. Listed → exists, and lives in the module it claims.
	tree := packageDirs(t)
	for _, p := range idx.Packages {
		mod, ok := tree[p.Import]
		if !ok {
			t.Errorf("%s listed in capabilities.yaml but no Go package directory exists", p.Import)
			continue
		}
		want := p.Module
		if want == "" {
			want = idx.Framework
		}
		if mod != want {
			t.Errorf("%s: capabilities.yaml says module %q, tree says %q", p.Import, want, mod)
		}
	}

	// 2. Exists → listed.
	for imp := range tree {
		if _, ok := idx.Lookup(imp); !ok {
			t.Errorf("%s exists in the tree but is missing from capabilities.yaml", imp)
		}
	}

	// 3. Every declared module is real.
	for _, m := range idx.Modules {
		rel := strings.TrimPrefix(m, idx.Framework+"/")
		if _, err := os.Stat(filepath.Join(filepath.FromSlash(rel), "go.mod")); err != nil {
			t.Errorf("module %s declared but %s/go.mod not found", m, rel)
		}
	}
}

// packageDirs walks the repository and returns import path → module path for
// every directory holding non-test Go files that a consumer may import:
// internal/, cmd/, examples/, testdata, and dot/underscore dirs are excluded.
func packageDirs(t *testing.T) map[string]string {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rootMod, err := astx.ParseGoMod("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]string)
	modules := map[string]string{".": rootMod.Module} // dir (slash, relative) → module path
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			name := d.Name()
			if rel != "." && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "vendor" || name == "testdata" || name == "node_modules" || name == "internal" || name == "cmd" || name == "examples") {
				return filepath.SkipDir
			}
			if rel != "." {
				if gm, parseErr := astx.ParseGoMod(filepath.Join(path, "go.mod")); parseErr == nil {
					modules[rel] = gm.Module
				}
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		dir := filepath.ToSlash(filepath.Dir(rel))
		modDir, modPath := nearestModule(dir, modules)
		imp := modPath
		if sub := strings.TrimPrefix(strings.TrimPrefix(dir, modDir), "/"); sub != "" && dir != modDir {
			imp = modPath + "/" + sub
		}
		if dir == "." {
			imp = modPath
		}
		out[imp] = modPath
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	delete(out, rootMod.Module) // the root package itself is the umbrella, not a capability
	return out
}

func nearestModule(dir string, modules map[string]string) (string, string) {
	best, bestMod := ".", modules["."]
	for d, m := range modules {
		if d == "." {
			continue
		}
		if (dir == d || strings.HasPrefix(dir, d+"/")) && len(d) > len(best) {
			best, bestMod = d, m
		}
	}
	return best, bestMod
}
