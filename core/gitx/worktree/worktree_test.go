// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package worktree_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/core/gitx"
	"github.com/golusoris/golusoris/core/gitx/worktree"
)

func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	r := gitx.New(dir)
	ctx := t.Context()
	for _, s := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@example.invalid"},
		{"config", "user.name", "t"},
		{"config", "commit.gpgsign", "false"},
	} {
		if _, err := r.Run(ctx, s...); err != nil {
			t.Fatalf("git %v: %v", s, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o600); err != nil {
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

func TestLifecycle(t *testing.T) {
	t.Parallel()
	root := initRepo(t)
	m := worktree.New(root, worktree.WithDir(".standards/worktrees"), worktree.WithBranchPrefix("wt/"))
	ctx := t.Context()

	wt, err := m.Create(ctx, "task-1", "main")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if wt.Branch != "wt/task-1" || wt.Path != m.Path("task-1") {
		t.Fatalf("unexpected worktree: %+v", wt)
	}
	if _, statErr := os.Stat(filepath.Join(wt.Path, "f.txt")); statErr != nil {
		t.Fatalf("worktree not checked out: %v", statErr)
	}
	list, err := m.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected main + 1 worktree, got %d: %+v", len(list), list)
	}
	var found bool
	for _, i := range list {
		if i.Branch == "wt/task-1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("task branch missing from list: %+v", list)
	}
	if _, err = m.Create(ctx, "task-1", "main"); err == nil {
		t.Fatal("duplicate Create must fail")
	}
	if err = m.Remove(ctx, "task-1", false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, statErr := os.Stat(wt.Path); !os.IsNotExist(statErr) {
		t.Fatalf("worktree dir still present: %v", statErr)
	}
	if err = m.Prune(ctx); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	list, err = m.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("expected only main after remove, got %+v", list)
	}
}

func TestValidation(t *testing.T) {
	t.Parallel()
	root := initRepo(t)
	m := worktree.New(root)
	ctx := t.Context()
	tests := []struct {
		name, id, base string
		want           error
	}{
		{"empty id", "", "main", worktree.ErrEmptyTaskID},
		{"too long", strings.Repeat("a", worktree.MaxTaskIDLen+1), "main", worktree.ErrTaskIDTooLong},
		{"path traversal", "../etc", "main", worktree.ErrInvalidTaskID},
		{"bad base", "ok", "ma in", worktree.ErrInvalidBase},
		{"option injection base", "ok", "--exec=x", worktree.ErrInvalidBase},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := m.Create(ctx, tc.id, tc.base); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
	if err := m.Remove(ctx, "nope/x", false); !errors.Is(err, worktree.ErrInvalidTaskID) {
		t.Fatalf("Remove bad id: %v", err)
	}
}

func TestParseList(t *testing.T) {
	t.Parallel()
	out := "worktree /repo\nHEAD aaaa\nbranch refs/heads/main\n\nworktree /repo/.wt/t1\nHEAD bbbb\nbranch refs/heads/wt/t1\nlocked agent busy\n\nworktree /repo/.wt/t2\nHEAD cccc\ndetached\nprunable gitdir file points to non-existent location\n"
	infos, err := worktree.ParseList(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 3 {
		t.Fatalf("got %d records", len(infos))
	}
	if infos[0].Branch != "main" || infos[0].Ref != "refs/heads/main" {
		t.Errorf("record 0: %+v", infos[0])
	}
	if !infos[1].Locked || infos[1].LockReason != "agent busy" || infos[1].Branch != "wt/t1" {
		t.Errorf("record 1: %+v", infos[1])
	}
	if !infos[2].Detached || !infos[2].Prunable || infos[2].PruneReason == "" {
		t.Errorf("record 2: %+v", infos[2])
	}
	if got, err := worktree.ParseList(""); err != nil || len(got) != 0 {
		t.Errorf("empty input: %+v %v", got, err)
	}
	if _, err := worktree.ParseList(strings.Repeat("\n", worktree.MaxPorcelainLines+1)); !errors.Is(err, worktree.ErrTooManyLines) {
		t.Errorf("line bound: %v", err)
	}
}
