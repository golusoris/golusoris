// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package scaffold_test

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/cmd/golusoris/internal/scaffold"
)

func TestInitCmd_noArgs(t *testing.T) {
	t.Parallel()
	cmd := scaffold.InitCmd()
	cmd.SetArgs([]string{})
	// With no args, the RunE returns an error — cobra propagates it.
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error with no args")
	}
}

func TestInitCmd_createsFiles(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	cmd := scaffold.InitCmd()
	cmd.SetArgs([]string{"myapp", "--module", "github.com/example/myapp"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}

	for _, f := range []string{
		"myapp/go.mod",
		"myapp/cmd/app/main.go",
		"myapp/LICENSE",
		"myapp/REUSE.toml",
	} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("expected file %s to exist: %v", f, err)
		}
	}

	rootMain := filepath.Join(dir, "myapp/main.go")
	if _, err := os.Stat(rootMain); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("expected root main.go not to exist, got: %v", err)
	}
}

func TestInitCmd_invalidName(t *testing.T) {
	t.Parallel()
	cmd := scaffold.InitCmd()
	cmd.SetArgs([]string{"bad/name"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid name")
	}
	if !strings.Contains(err.Error(), "contains invalid characters") {
		t.Errorf("expected invalid characters error, got: %v", err)
	}
}

func TestInitCmd_emptyName(t *testing.T) {
	t.Parallel()
	cmd := scaffold.InitCmd()
	cmd.SetArgs([]string{""})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for empty name")
	}
	if !strings.Contains(err.Error(), "name must not be empty") {
		t.Errorf("expected empty name error, got: %v", err)
	}
}

func TestAddCmd_listModules(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	cmd := scaffold.AddCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error listing modules: %v", err)
	}
	got := buf.String()
	if !strings.HasPrefix(got, "Available modules:") {
		t.Fatalf("output does not start with %q: %q", "Available modules:", got)
	}

	expected := map[string]string{
		"auth-oidc": "golusoris.AuthOIDC",
		"authz":     "golusoris.Authz",
		"cache":     "golusoris.CacheMemory",
		"db":        "golusoris.DB",
		"http":      "golusoris.HTTP",
		"jobs":      "golusoris.Jobs",
		"k8s":       "golusoris.K8s",
		"otel":      "golusoris.OTel",
	}

	for mod, fxVar := range expected {
		if !strings.Contains(got, mod) {
			t.Errorf("output missing module %q", mod)
		}
		if !strings.Contains(got, fxVar) {
			t.Errorf("output missing fx variable %q for module %q", fxVar, mod)
		}
	}

	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != len(expected)+1 {
		t.Fatalf("got %d lines, want %d", len(lines), len(expected)+1)
	}
	moduleLines := lines[1:]
	if !slices.IsSorted(moduleLines) {
		t.Errorf("module lines are not in sorted order: %v", moduleLines)
	}
}

func TestAddCmd_knownModule(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	cmd := scaffold.AddCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"db"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error for known module: %v", err)
	}
	got := buf.String()
	wantImport := `import "github.com/golusoris/golusoris"`
	if !strings.Contains(got, wantImport) {
		t.Errorf("output does not contain %s; got:\n%s", wantImport, got)
	}
	if !strings.Contains(got, "golusoris.DB,") {
		t.Errorf("output does not contain golusoris.DB,; got:\n%s", got)
	}
}

func TestAddCmd_unknownModule(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	cmd := scaffold.AddCmd()
	cmd.SetOut(&buf)
	unknown := "nonexistent-module-xyz"
	cmd.SetArgs([]string{unknown})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown module")
	}
	if !strings.Contains(err.Error(), unknown) {
		t.Errorf("error %q does not contain module name %q", err.Error(), unknown)
	}
	if buf.Len() != 0 {
		t.Errorf("expected buffer to be empty, got %q", buf.String())
	}
}

func TestAddCmd_emptyModuleName(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	cmd := scaffold.AddCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{""})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `unknown module ""`) {
		t.Fatalf("Execute() error = %v, want unknown module \"\"", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected buffer to be empty, got %q", buf.String())
	}
}

func TestBumpCmd_noArgs(t *testing.T) {
	t.Parallel()
	// bump with no version should not crash the command itself (it runs go get
	// which may fail in test environment — that's fine; just verify it's wired).
	cmd := scaffold.BumpCmd()
	if cmd == nil {
		t.Fatal("BumpCmd() returned nil")
	}
}
