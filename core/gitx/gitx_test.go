// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gitx_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/golusoris/golusoris/core/gitx"
)

// initRepo creates a throwaway repository with one commit and returns its path.
func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	r := gitx.New(dir)
	ctx := t.Context()
	steps := [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@example.invalid"},
		{"config", "user.name", "t"},
		{"config", "commit.gpgsign", "false"},
		{"remote", "add", "origin", "https://github.com/golusoris/golusoris.git"},
	}
	for _, s := range steps {
		if _, err := r.Run(ctx, s...); err != nil {
			t.Fatalf("git %v: %v", s, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(ctx, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(ctx, "commit", "-q", "-m", "init"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRepoFacts(t *testing.T) {
	t.Parallel()
	dir := initRepo(t)
	r := gitx.New(dir)
	ctx := t.Context()

	head, err := r.Head(ctx)
	if err != nil || len(head) != 40 {
		t.Fatalf("Head: %q %v", head, err)
	}
	br, err := r.Branch(ctx)
	if err != nil || br != "main" {
		t.Fatalf("Branch: %q %v", br, err)
	}
	url, err := r.RemoteURL(ctx, "origin")
	if err != nil || url != "https://github.com/golusoris/golusoris.git" {
		t.Fatalf("RemoteURL: %q %v", url, err)
	}
	dirty, err := r.IsDirty(ctx)
	if err != nil || dirty {
		t.Fatalf("IsDirty on clean repo: %v %v", dirty, err)
	}
	if err = os.WriteFile(filepath.Join(dir, "new.txt"), []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	dirty, err = r.IsDirty(ctx)
	if err != nil || !dirty {
		t.Fatalf("IsDirty with untracked file: %v %v", dirty, err)
	}
	top, err := r.TopLevel(ctx)
	if err != nil || top == "" {
		t.Fatalf("TopLevel: %q %v", top, err)
	}
}

func TestRunErrors(t *testing.T) {
	t.Parallel()
	dir := initRepo(t)
	r := gitx.New(dir)
	ctx := t.Context()
	if _, err := r.Run(ctx, "rev-parse", "no\x00pe"); !errors.Is(err, gitx.ErrInvalidArg) {
		t.Fatalf("NUL arg: got %v", err)
	}
	if _, err := r.Run(ctx, "definitely-not-a-git-command"); err == nil {
		t.Fatal("expected failure for unknown subcommand")
	}
	if _, err := r.RemoteURL(ctx, "--upload-pack=evil"); !errors.Is(err, gitx.ErrInvalidArg) {
		t.Fatalf("option-injection remote name: got %v", err)
	}
	small := gitx.New(dir, gitx.WithMaxOutput(8))
	if _, err := small.Run(ctx, "log", "--format=%H%H%H"); !errors.Is(err, gitx.ErrOutputTooBig) {
		t.Fatalf("output bound: got %v", err)
	}
}

func TestValidRef(t *testing.T) {
	t.Parallel()
	tests := map[string]bool{
		"main": true, "feat/core-1": true, "wt/task_42": true, "origin": true,
		"": false, "-x": false, "a..b": false, "a b": false, "a~1": false, "x.lock": false,
		"feat/": false, "/x": false, "a//b": false, "HEAD@{1}": false, "tab\tx": false,
	}
	for in, want := range tests {
		if got := gitx.ValidRef(in); got != want {
			t.Errorf("ValidRef(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseRemote(t *testing.T) {
	t.Parallel()
	tests := []struct {
		url         string
		owner, repo string
		ok          bool
	}{
		{"https://github.com/golusoris/golusoris.git", "golusoris", "golusoris", true},
		{"https://github.com/cordanallm/praetor", "cordanallm", "praetor", true},
		{"git@github.com:lusoris/arca.git", "lusoris", "arca", true},
		{"ssh://git@gitea.cauda.dev:2222/golusoris/goenvoy.git", "golusoris", "goenvoy", true},
		{"https://user@host/o/r/", "o", "r", true},
		{"not a url", "", "", false},
		{"https://github.com/only-owner", "", "", false},
	}
	for _, tc := range tests {
		o, r, ok := gitx.ParseRemote(tc.url)
		if o != tc.owner || r != tc.repo || ok != tc.ok {
			t.Errorf("ParseRemote(%q) = %q %q %v, want %q %q %v", tc.url, o, r, ok, tc.owner, tc.repo, tc.ok)
		}
	}
}
