// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package astx

import (
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
)

func TestWalkBudget_positiveOverride(t *testing.T) {
	t.Parallel()
	if got := walkBudget(7); got != 7 {
		t.Errorf("walkBudget(7) = %d, want 7", got)
	}
}

func TestWalkBudget_zeroUsesDefault(t *testing.T) {
	t.Parallel()
	if got := walkBudget(0); got != DefaultMaxFiles {
		t.Errorf("walkBudget(0) = %d, want %d", got, DefaultMaxFiles)
	}
}

func TestWalkBudget_negativeUsesDefault(t *testing.T) {
	t.Parallel()
	if got := walkBudget(-1); got != DefaultMaxFiles {
		t.Errorf("walkBudget(-1) = %d, want %d", got, DefaultMaxFiles)
	}
}

func TestWalkSkipSet_includesDefaults(t *testing.T) {
	t.Parallel()
	skip := walkSkipSet(nil)
	for _, d := range []string{"vendor", "testdata", "node_modules"} {
		if !skip[d] {
			t.Errorf("walkSkipSet(nil)[%q] = false, want true", d)
		}
	}
}

func TestWalkSkipSet_mergesExtra(t *testing.T) {
	t.Parallel()
	skip := walkSkipSet([]string{"dist", "build"})
	for _, d := range []string{"vendor", "dist", "build"} {
		if !skip[d] {
			t.Errorf("walkSkipSet([dist,build])[%q] = false, want true", d)
		}
	}
	if skip["not-skipped"] {
		t.Error(`walkSkipSet(...)["not-skipped"] = true, want false`)
	}
}

func TestWalkSkipSet_doesNotMutateDefaults(t *testing.T) {
	t.Parallel()
	_ = walkSkipSet([]string{"only-here"})
	if defaultSkipDirs["only-here"] {
		t.Error("walkSkipSet leaked a caller-supplied entry into defaultSkipDirs")
	}
}

func TestVisitDir_skipsMatchingSubdir(t *testing.T) {
	t.Parallel()
	w := &walker{root: "/repo", skip: map[string]bool{"vendor": true}}
	err := w.visitDir("/repo/vendor", fakeDirEntry{name: "vendor", isDir: true})
	if !errors.Is(err, filepath.SkipDir) {
		t.Errorf("visitDir(vendor) = %v, want filepath.SkipDir", err)
	}
}

func TestVisitDir_descendsNonSkippedSubdir(t *testing.T) {
	t.Parallel()
	w := &walker{root: "/repo", skip: map[string]bool{"vendor": true}}
	if err := w.visitDir("/repo/sub", fakeDirEntry{name: "sub", isDir: true}); err != nil {
		t.Errorf("visitDir(sub) = %v, want nil", err)
	}
}

// TestVisitDir_neverSkipsRoot is the boundary case: the walk root's own
// basename could coincidentally collide with a skip-dir name (e.g. a repo
// checked out as "vendor"); the root itself must never be skipped.
func TestVisitDir_neverSkipsRoot(t *testing.T) {
	t.Parallel()
	w := &walker{root: "/repo/vendor", skip: map[string]bool{"vendor": true}}
	if err := w.visitDir("/repo/vendor", fakeDirEntry{name: "vendor", isDir: true}); err != nil {
		t.Errorf("visitDir(root) = %v, want nil (root is never skipped)", err)
	}
}

// fakeDirEntry is a minimal fs.DirEntry stub for unit-testing visitDir
// without touching the filesystem.
type fakeDirEntry struct {
	name  string
	isDir bool
}

func (f fakeDirEntry) Name() string { return f.name }

func (f fakeDirEntry) IsDir() bool { return f.isDir }

func (f fakeDirEntry) Type() fs.FileMode {
	if f.isDir {
		return fs.ModeDir
	}
	return 0
}

func (f fakeDirEntry) Info() (fs.FileInfo, error) {
	return nil, fs.ErrInvalid // unused by visitDir; never called in these tests
}
