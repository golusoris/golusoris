// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package clikit_test

import (
	"bytes"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/core/clikit"
)

// goldenDir holds the checked-in output of buildTree; regenerate with
// GOLUSORIS_UPDATE_GOLDEN=1 go test ./clikit/.
const goldenDir = "testdata/golden"

func generate(t *testing.T, withPort bool) map[string][]byte {
	t.Helper()
	files, err := clikit.Generate(buildTree(withPort), clikit.ManOptions{Source: "myapp 1.0.0", Manual: "Myapp Manual"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return files
}

func TestGenerateGolden(t *testing.T) {
	t.Parallel()
	files := generate(t, true)
	if os.Getenv("GOLUSORIS_UPDATE_GOLDEN") == "1" {
		if err := os.RemoveAll(goldenDir); err != nil {
			t.Fatal(err)
		}
		if err := clikit.WriteFiles(goldenDir, files); err != nil {
			t.Fatal(err)
		}
	}
	if err := clikit.CheckDrift(goldenDir, files); err != nil {
		t.Fatalf("golden drift (GOLUSORIS_UPDATE_GOLDEN=1 regenerates): %v", err)
	}
}

func TestGenerateLayout(t *testing.T) {
	t.Parallel()
	got := slices.Sorted(maps.Keys(generate(t, true)))
	want := []string{
		"completions/bash/myapp",
		"completions/fish/myapp.fish",
		"completions/powershell/myapp.ps1",
		"completions/zsh/_myapp",
		"man/man1/myapp-completion-bash.1",
		"man/man1/myapp-completion-fish.1",
		"man/man1/myapp-completion-powershell.1",
		"man/man1/myapp-completion-zsh.1",
		"man/man1/myapp-completion.1",
		"man/man1/myapp-db-migrate.1",
		"man/man1/myapp-db.1",
		"man/man1/myapp-serve.1",
		"man/man1/myapp.1",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Generate keys:\n got %q\nwant %q", got, want)
	}
}

func TestGenerateIsReproducible(t *testing.T) {
	t.Parallel()
	a, b := generate(t, true), generate(t, true)
	for name, data := range a {
		if !bytes.Equal(data, b[name]) {
			t.Errorf("%s differs between two runs", name)
		}
	}
}

// TestRemovedFlagDrifts is the #630 acceptance: dropping a flag removes it
// from every output, and the drift helper fails on the stale copy.
func TestRemovedFlagDrifts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := clikit.WriteFiles(dir, generate(t, true)); err != nil {
		t.Fatal(err)
	}
	if with := generate(t, true)["man/man1/myapp-serve.1"]; !bytes.Contains(with, []byte(`\-\-port`)) {
		t.Fatal("fixture lost --port; the absence check below would prove nothing")
	}
	without := generate(t, false)
	for name, data := range without {
		if bytes.Contains(data, []byte("--port")) || bytes.Contains(data, []byte(`\-\-port`)) {
			t.Errorf("%s still mentions the removed --port flag", name)
		}
	}
	err := clikit.CheckDrift(dir, without)
	if !errors.Is(err, clikit.ErrDrift) {
		t.Fatalf("CheckDrift = %v, want ErrDrift", err)
	}
	if !strings.Contains(err.Error(), "stale: man/man1/myapp-serve.1") {
		t.Errorf("drift error does not name the stale page: %v", err)
	}
}

func TestCheckDrift(t *testing.T) {
	t.Parallel()
	files := map[string][]byte{"a/one.txt": []byte("1\n"), "two.txt": []byte("2\n")}
	cases := []struct {
		name   string
		mutate func(dir string) error
		want   string
	}{
		{"clean", func(string) error { return nil }, ""},
		{"stale", func(dir string) error { return write(dir, "two.txt", "changed\n") }, "stale: two.txt"},
		{"missing", func(dir string) error { return os.Remove(filepath.Join(dir, "a", "one.txt")) }, "missing: a/one.txt"},
		{"unexpected", func(dir string) error { return write(dir, "a/old.1", "x") }, "unexpected: a/old.1"},
		{"empty-file", func(dir string) error { return write(dir, "two.txt", "") }, "stale: two.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if err := clikit.WriteFiles(dir, files); err != nil {
				t.Fatal(err)
			}
			if err := tc.mutate(dir); err != nil {
				t.Fatal(err)
			}
			err := clikit.CheckDrift(dir, files)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("CheckDrift = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, clikit.ErrDrift) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("CheckDrift = %v, want ErrDrift naming %q", err, tc.want)
			}
		})
	}
}

func TestCheckDriftMissingDir(t *testing.T) {
	t.Parallel()
	err := clikit.CheckDrift(filepath.Join(t.TempDir(), "absent"), map[string][]byte{"x": []byte("x")})
	if !errors.Is(err, clikit.ErrDrift) || !strings.Contains(err.Error(), "missing: x") {
		t.Fatalf("CheckDrift = %v, want missing x", err)
	}
}

func TestCheckDriftEmptySetOnEmptyDir(t *testing.T) {
	t.Parallel()
	if err := clikit.CheckDrift(t.TempDir(), nil); err != nil {
		t.Fatalf("CheckDrift(empty) = %v", err)
	}
}

func TestFilesRejectNonLocalNames(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"", "../escape", "/abs/path", `back\slash`, "a/../../b"} {
		files := map[string][]byte{name: []byte("x")}
		if err := clikit.WriteFiles(t.TempDir(), files); err == nil {
			t.Errorf("WriteFiles(%q) = nil, want error", name)
		}
		if err := clikit.CheckDrift(t.TempDir(), files); err == nil || errors.Is(err, clikit.ErrDrift) {
			t.Errorf("CheckDrift(%q) = %v, want a name error", name, err)
		}
	}
}

func write(dir, name, data string) error {
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(data), 0o600)
}
