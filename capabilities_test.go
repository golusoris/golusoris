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

// verifyPackagesInTree checks that listed packages exist and belong to the claimed module.
func verifyPackagesInTree(t *testing.T, idx *capabilities.Index, tree map[string]string) {
	t.Helper()
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
}

// verifyTreePackagesListed checks that all discovered Go packages appear in capabilities.yaml.
func verifyTreePackagesListed(t *testing.T, idx *capabilities.Index, tree map[string]string) {
	t.Helper()
	for imp := range tree {
		if _, ok := idx.Lookup(imp); !ok {
			t.Errorf("%s exists in the tree but is missing from capabilities.yaml", imp)
		}
	}
}

// verifyDeclaredModules checks that each declared submodule has a real go.mod file.
func verifyDeclaredModules(t *testing.T, idx *capabilities.Index) {
	t.Helper()
	for _, m := range idx.Modules {
		rel := strings.TrimPrefix(m, idx.Framework+"/")
		if _, err := os.Stat(filepath.Join(filepath.FromSlash(rel), "go.mod")); err != nil {
			t.Errorf("module %s declared but %s/go.mod not found", m, rel)
		}
	}
}

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

	tree := packageDirs(t)
	verifyPackagesInTree(t, idx, tree)
	verifyTreePackagesListed(t, idx, tree)
	verifyDeclaredModules(t, idx)
}

func shouldSkipDir(rel, name string) bool {
	if rel == "." {
		return false
	}
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
		return true
	}
	switch name {
	case "vendor", "testdata", "node_modules", "internal", "cmd", "examples":
		return true
	}
	return rel == "tools"
}

func isSourceGoFile(name string) bool {
	return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
}

func computeImportPath(dir string, modules map[string]string) (string, string) {
	modDir, modPath := nearestModule(dir, modules)
	if dir == "." || dir == modDir {
		return modPath, modPath
	}
	sub := strings.TrimPrefix(strings.TrimPrefix(dir, modDir), "/")
	return modPath + "/" + sub, modPath
}

type packageDirsWalker struct {
	root    string
	modules map[string]string
	out     map[string]string
}

func (w *packageDirsWalker) walkDir(rel, path, name string) error {
	if shouldSkipDir(rel, name) {
		return filepath.SkipDir
	}
	if rel != "." {
		if gm, parseErr := astx.ParseGoMod(filepath.Join(path, "go.mod")); parseErr == nil {
			w.modules[rel] = gm.Module
		}
	}
	return nil
}

func (w *packageDirsWalker) walk(path string, d fs.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	rel, _ := filepath.Rel(w.root, path)
	rel = filepath.ToSlash(rel)
	if d.IsDir() {
		return w.walkDir(rel, path, d.Name())
	}
	if !isSourceGoFile(d.Name()) {
		return nil
	}
	dir := filepath.ToSlash(filepath.Dir(rel))
	imp, modPath := computeImportPath(dir, w.modules)
	w.out[imp] = modPath
	return nil
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
	walker := &packageDirsWalker{
		root:    root,
		modules: map[string]string{".": rootMod.Module},
		out:     make(map[string]string),
	}
	if err = filepath.WalkDir(root, walker.walk); err != nil {
		t.Fatal(err)
	}
	delete(walker.out, rootMod.Module) // the root package itself is the umbrella, not a capability
	return walker.out
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
