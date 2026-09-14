// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package astx_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/core/astx"
)

func write(t *testing.T, root, rel, body string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWalkFiltersAndBounds(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, root, "a.go", "package a\n")
	write(t, root, "a_test.go", "package a\n")
	write(t, root, "sub/b.go", "package sub\n")
	write(t, root, "vendor/v.go", "package v\n")
	write(t, root, ".hidden/h.go", "package h\n")
	write(t, root, "_skip/s.go", "package s\n")
	write(t, root, "testdata/t.go", "package t\n")
	write(t, root, "notgo.txt", "x")

	var got []string
	err := astx.Walk(t.Context(), root, astx.WalkOptions{}, func(p string) error {
		rel, _ := filepath.Rel(root, p)
		got = append(got, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if want := []string{"a.go", "sub/b.go"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v want %v", got, want)
	}

	var withTests int
	err = astx.Walk(t.Context(), root, astx.WalkOptions{IncludeTests: true}, func(string) error { withTests++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if withTests != 3 {
		t.Fatalf("IncludeTests: got %d files", withTests)
	}

	err = astx.Walk(t.Context(), root, astx.WalkOptions{MaxFiles: 1}, func(string) error { return nil })
	if !errors.Is(err, astx.ErrTooManyFiles) {
		t.Fatalf("budget: got %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := astx.Walk(ctx, root, astx.WalkOptions{}, func(string) error { return nil }); err == nil {
		t.Fatal("cancelled context must abort the walk")
	}
}

func TestImportsAndThirdParty(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	p := write(t, root, "m.go", "package m\n\nimport (\n\t\"fmt\"\n\t\"example.com/app/internal/x\"\n\tyaml \"gopkg.in/yaml.v3\"\n)\n\nvar _ = fmt.Sprint\nvar _ = x.X\nvar _ = yaml.Marshal\n")
	imps, err := astx.Imports(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(imps) != 3 {
		t.Fatalf("imports: %v", imps)
	}
	var third []string
	for _, i := range imps {
		if astx.IsThirdParty(i, "example.com/app") {
			third = append(third, i)
		}
	}
	if len(third) != 1 || third[0] != "gopkg.in/yaml.v3" {
		t.Fatalf("third-party: %v", third)
	}
	if _, err := astx.Imports(filepath.Join(root, "missing.go")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestResolveAndRewriteImports(t *testing.T) {
	t.Parallel()
	mapping := map[string]string{
		"github.com/golusoris/golusoris/config": "github.com/golusoris/golusoris/core/config",
		"github.com/golusoris/golusoris/clikit": "github.com/golusoris/golusoris/core/clikit",
		"gopkg.in/yaml.v3":                      "github.com/golusoris/golusoris/core/codec/yaml",
	}
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"github.com/golusoris/golusoris/config", "github.com/golusoris/golusoris/core/config", true},
		{"github.com/golusoris/golusoris/clikit/tui", "github.com/golusoris/golusoris/core/clikit/tui", true},
		{"github.com/golusoris/golusoris/configx", "github.com/golusoris/golusoris/configx", false},
		{"fmt", "fmt", false},
	}
	for _, tc := range tests {
		if got, ok := astx.Resolve(tc.in, mapping); got != tc.want || ok != tc.ok {
			t.Errorf("Resolve(%q) = %q %v, want %q %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}

	src := []byte("// Package main does things.\npackage main\n\nimport (\n\t\"fmt\"\n\n\tcfg \"github.com/golusoris/golusoris/config\" // keep alias\n\t\"gopkg.in/yaml.v3\"\n)\n\nfunc main() { fmt.Println(cfg.Module, yaml.Marshal) }\n")
	out, changed, err := astx.RewriteImports(src, mapping)
	if err != nil || !changed {
		t.Fatalf("rewrite: changed=%v err=%v", changed, err)
	}
	s := string(out)
	for _, want := range []string{`cfg "github.com/golusoris/golusoris/core/config" // keep alias`, `"github.com/golusoris/golusoris/core/codec/yaml"`, "// Package main does things."} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "gopkg.in") {
		t.Errorf("old import survived:\n%s", s)
	}
	same, changed, err := astx.RewriteImports([]byte("package p\n"), mapping)
	if err != nil || changed || string(same) != "package p\n" {
		t.Fatalf("no-op rewrite altered source: %q %v %v", same, changed, err)
	}
	if _, _, err := astx.RewriteImports([]byte("package {"), mapping); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestRewriteImportsFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	p := write(t, root, "f.go", "package f\n\nimport \"github.com/golusoris/golusoris/log\"\n\nvar _ = log.New\n")
	mapping := map[string]string{"github.com/golusoris/golusoris/log": "github.com/golusoris/golusoris/core/log"}
	changed, err := astx.RewriteImportsFile(p, mapping)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "core/log") {
		t.Fatalf("file not rewritten:\n%s", data)
	}
	changed, err = astx.RewriteImportsFile(p, mapping)
	if err != nil || changed {
		t.Fatalf("second pass must be a no-op: changed=%v err=%v", changed, err)
	}
}

func TestFuncMetrics(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	p := write(t, root, "m.go", `package m

type T struct{}

func (t *T) M(a, b int) int {
	if a > b && b > 0 {
		return a
	}
	for i := 0; i < b; i++ {
		switch i {
		case 1:
			a++
		case 2, 3:
			a--
		default:
			a += 2
		}
	}
	return a
}

func plain() {}

func decl()
`)
	ms, err := astx.FuncMetrics(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 {
		t.Fatalf("expected 2 bodies, got %d: %+v", len(ms), ms)
	}
	m := ms[0]
	if m.Name != "T.M" || m.Params != 2 || m.Line != 5 || m.Lines != 16 {
		t.Errorf("shape: %+v", m)
	}
	// 1 + if + && + for + case 1 + case 2,3 = 6 (default adds nothing).
	if m.Cyclomatic != 6 {
		t.Errorf("cyclomatic = %d, want 6", m.Cyclomatic)
	}
	if m.Statements < 8 {
		t.Errorf("statements = %d, want >= 8", m.Statements)
	}
	if ms[1].Name != "plain" || ms[1].Cyclomatic != 1 || ms[1].Lines != 1 {
		t.Errorf("plain: %+v", ms[1])
	}
}

func TestParseGoMod(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	p := write(t, root, "go.mod", "module github.com/cordanallm/praetor\n\ngo 1.27\n\nrequire (\n\tgopkg.in/yaml.v3 v3.0.1\n\tgolang.org/x/sys v0.1.0 // indirect\n)\n")
	gm, err := astx.ParseGoMod(p)
	if err != nil {
		t.Fatal(err)
	}
	if gm.Module != "github.com/cordanallm/praetor" || gm.Go != "1.27" || len(gm.Require) != 2 {
		t.Fatalf("%+v", gm)
	}
	d := gm.Direct()
	if len(d) != 1 || d[0].Path != "gopkg.in/yaml.v3" || d[0].Version != "v3.0.1" {
		t.Fatalf("direct: %+v", d)
	}
	if _, err := astx.ParseGoMod(write(t, root, "bad/go.mod", "module \"unterminated\n")); err == nil {
		t.Fatal("expected parse error")
	}
}
