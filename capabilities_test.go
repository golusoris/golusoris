// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package golusoris_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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

func capabilitiesReplacementProblems(idx *capabilities.Index) []string {
	wrappers := make(map[string][]string)
	for _, p := range idx.Packages {
		for _, module := range p.Wraps {
			wrappers[module] = append(wrappers[module], p.Import)
		}
	}
	var problems []string
	for module, claimants := range idx.ReplacementCandidates() {
		if len(claimants) > 1 {
			problems = append(problems, fmt.Sprintf("%s: multiple replaces claimants: %s", module, strings.Join(claimants, ", ")))
		}
		if wrappedBy := wrappers[module]; len(wrappedBy) > 0 {
			slices.Sort(wrappedBy)
			problems = append(problems, fmt.Sprintf("%s: replaces claimants: %s; wraps claimants: %s", module, strings.Join(claimants, ", "), strings.Join(wrappedBy, ", ")))
		}
	}
	slices.Sort(problems)
	return problems
}

func TestCapabilitiesReplacements(t *testing.T) {
	t.Parallel()
	idx, err := capabilities.Load(capabilities.FileName)
	if err != nil {
		t.Fatalf("load %s: %v", capabilities.FileName, err)
	}
	for _, problem := range capabilitiesReplacementProblems(idx) {
		t.Error(problem)
	}
}

func TestCapabilitiesReplacementProblems(t *testing.T) {
	t.Parallel()
	const module = "example.org/library"
	const first = "github.com/golusoris/golusoris/a"
	const second = "github.com/golusoris/golusoris/b"
	tests := []struct {
		name     string
		packages []capabilities.Package
		want     []string
	}{
		{"single claimant", []capabilities.Package{{Import: first, Replaces: []string{module}}}, nil},
		{"two wrappers", []capabilities.Package{{Import: first, Wraps: []string{module}}, {Import: second, Wraps: []string{module}}}, nil},
		{
			"second claimant",
			[]capabilities.Package{{Import: second, Replaces: []string{module}}, {Import: first, Replaces: []string{module}}},
			[]string{module + ": multiple replaces claimants: " + first + ", " + second},
		},
		{
			"replaced and wrapped",
			[]capabilities.Package{{Import: first, Replaces: []string{module}}, {Import: second, Wraps: []string{module}}},
			[]string{module + ": replaces claimants: " + first + "; wraps claimants: " + second},
		},
		{"no packages", nil, nil},
		{"no lists", []capabilities.Package{{Import: first}, {Import: second}}, nil},
		{
			"sorted problems",
			[]capabilities.Package{{Import: second, Replaces: []string{"z.example/lib", module}}, {Import: first, Replaces: []string{module, "z.example/lib"}}},
			[]string{module + ": multiple replaces claimants: " + first + ", " + second, "z.example/lib: multiple replaces claimants: " + first + ", " + second},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := capabilitiesReplacementProblems(&capabilities.Index{Packages: tc.packages})
			if !slices.Equal(got, tc.want) {
				t.Fatalf("problems = %v, want %v", got, tc.want)
			}
		})
	}
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
