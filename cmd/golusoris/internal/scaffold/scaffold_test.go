package scaffold_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/golusoris/golusoris/cmd/golusoris/internal/scaffold"
)

func TestInitCmd_noArgs(t *testing.T) {
	cmd := scaffold.InitCmd()
	cmd.SetArgs([]string{})
	// With no args, the RunE returns an error — cobra propagates it.
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error with no args")
	}
}

func TestInitCmd_createsFiles(t *testing.T) {
	dir := t.TempDir()
	origWD, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	cmd := scaffold.InitCmd()
	cmd.SetArgs([]string{"myapp", "--module", "github.com/example/myapp"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}

	for _, f := range []string{"myapp/go.mod", "myapp/main.go"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("expected file %s to exist: %v", f, err)
		}
	}
}

func TestAddCmd_listModules(t *testing.T) {
	cmd := scaffold.AddCmd()
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error listing modules: %v", err)
	}
}

func TestAddCmd_knownModule(t *testing.T) {
	cmd := scaffold.AddCmd()
	cmd.SetArgs([]string{"db"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error for known module: %v", err)
	}
}

func TestAddCmd_unknownModule(t *testing.T) {
	cmd := scaffold.AddCmd()
	cmd.SetArgs([]string{"nonexistent-module-xyz"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for unknown module")
	}
}

func TestBumpCmd_noArgs(t *testing.T) {
	// bump with no version should not crash the command itself (it runs go get
	// which may fail in test environment — that's fine; just verify it's wired).
	cmd := scaffold.BumpCmd()
	if cmd == nil {
		t.Fatal("BumpCmd() returned nil")
	}
}
