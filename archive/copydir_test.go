// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package archive_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/golusoris/golusoris/archive"
)

// writeFile creates rel under root (creating parent dirs) with the given body
// and mode.
func writeFile(t *testing.T, root, rel, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

// relTree returns the sorted, slash-separated paths of every entry under
// root, relative to root. root itself is excluded.
func relTree(t *testing.T, root string) []string {
	t.Helper()
	var got []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == root {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		got = append(got, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	return got
}

func TestCopyDir_NestedTreeRoundTrip(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	writeFile(t, src, "a.txt", "hello", 0o640)
	writeFile(t, src, "sub/b.txt", "world", 0o640)
	writeFile(t, src, "sub/deeper/c.txt", "!", 0o640)
	restricted := writeFile(t, src, "restricted.txt", "secret", 0o600)
	if err := os.Chmod(restricted, 0o400); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "out")
	opts := archive.CopyOptions{PreservePermissions: true}
	if err := archive.CopyDir(t.Context(), src, dst, opts); err != nil {
		t.Fatalf("CopyDir: %v", err)
	}

	srcEntries, dstEntries := relTree(t, src), relTree(t, dst)
	if strings.Join(srcEntries, ",") != strings.Join(dstEntries, ",") {
		t.Fatalf("tree mismatch:\n src=%v\n dst=%v", srcEntries, dstEntries)
	}
	for _, rel := range []string{"a.txt", "sub/b.txt", "sub/deeper/c.txt"} {
		want, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dst, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("%s: content mismatch: got %q want %q", rel, got, want)
		}
	}

	info, err := os.Stat(filepath.Join(dst, "restricted.txt"))
	if err != nil {
		t.Fatal(err)
	}
	// Assert against the source, not a literal: Windows has no owner/group/
	// other split, so os.Chmod there only toggles the read-only attribute and
	// the 0400 written above reads back as 0444 on BOTH sides. Comparing the
	// two modes states the invariant the option actually promises, and still
	// pins the exact 0400 everywhere the platform can express it.
	srcInfo, err := os.Stat(restricted)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != srcInfo.Mode().Perm() {
		t.Fatalf("PreservePermissions: got mode %v want %v", info.Mode().Perm(), srcInfo.Mode().Perm())
	}
}

func TestCopyDir_PermissionsNotPreservedByDefault(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	p := writeFile(t, src, "f.txt", "x", 0o600)
	if err := os.Chmod(p, 0o400); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "out")
	if err := archive.CopyDir(t.Context(), src, dst, archive.CopyOptions{}); err != nil {
		t.Fatalf("CopyDir: %v", err)
	}

	info, err := os.Stat(filepath.Join(dst, "f.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() == 0o400 {
		t.Fatal("expected default (non-preserved) mode, got exact source mode 0400")
	}
}

// TestCopyDir_DefaultOptionsUsableOnReadOnlyDir guards the BLOCKING fix: a
// source directory that itself lacks the owner-write bit (e.g. an extracted
// archive or a vendored read-only tree) must still copy cleanly under the
// default (non-preserving) options, and the resulting copy must remain
// writable — a fresh entry must be creatable inside it. Before the fix,
// PreservePermissions=false mirrored the source directory's exact mode onto
// dst via the copy library's DoNothing permission control, which made dst's
// "readonly" directory read-only before its own contents were copied,
// failing with "permission denied" and leaving a partial tree.
func TestCopyDir_DefaultOptionsUsableOnReadOnlyDir(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	writeFile(t, src, "readonly/leaf.txt", "secret", 0o640)
	ro := filepath.Join(src, "readonly")
	if err := os.Chmod(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Restore write so t.TempDir's own cleanup can remove the tree.
		_ = os.Chmod(ro, 0o750)
	})

	dst := filepath.Join(t.TempDir(), "out")
	if err := archive.CopyDir(t.Context(), src, dst, archive.CopyOptions{}); err != nil {
		t.Fatalf("CopyDir with default options on a read-only source dir: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dst, "readonly", "leaf.txt"))
	if err != nil || string(got) != "secret" {
		t.Fatalf("leaf.txt not copied: err=%v got=%q", err, got)
	}

	// Subsequent write into the copied tree: the default (non-preserving)
	// copy must be a usable copy, not a read-only trap.
	newFile := filepath.Join(dst, "readonly", "new.txt")
	if err := os.WriteFile(newFile, []byte("added"), 0o640); err != nil {
		t.Fatalf("write into copied tree: %v", err)
	}
}

func TestCopyDir_SkipPredicate(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	writeFile(t, src, "keep.txt", "keep", 0o640)
	writeFile(t, src, "skip.tmp", "skip", 0o640)
	writeFile(t, src, "skipdir/x.txt", "x", 0o640)
	writeFile(t, src, "skipdir/y.txt", "y", 0o640)

	dst := filepath.Join(t.TempDir(), "out")
	opts := archive.CopyOptions{
		Skip: func(path string, info os.FileInfo) (bool, error) {
			name := info.Name()
			return name == "skip.tmp" || name == "skipdir", nil
		},
	}
	if err := archive.CopyDir(t.Context(), src, dst, opts); err != nil {
		t.Fatalf("CopyDir: %v", err)
	}

	got := relTree(t, dst)
	want := []string{"keep.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestCopyDir_SymlinkPolicy(t *testing.T) {
	t.Parallel()

	newSrc := func(t *testing.T) (root string, linkPath string) {
		t.Helper()
		root = t.TempDir()
		writeFile(t, root, "target/f.txt", "payload", 0o640)
		link := filepath.Join(root, "link")
		if err := os.Symlink(filepath.Join(root, "target"), link); err != nil {
			t.Skipf("symlinks unsupported: %v", err)
		}
		return root, link
	}

	t.Run("deep", func(t *testing.T) {
		t.Parallel()
		src, _ := newSrc(t)
		dst := filepath.Join(t.TempDir(), "out")
		opts := archive.CopyOptions{OnSymlink: archive.SymlinkDeep}
		if err := archive.CopyDir(t.Context(), src, dst, opts); err != nil {
			t.Fatalf("CopyDir: %v", err)
		}
		dstLink := filepath.Join(dst, "link")
		info, err := os.Lstat(dstLink)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			t.Fatal("deep: dst entry is still a symlink")
		}
		got, err := os.ReadFile(filepath.Join(dstLink, "f.txt"))
		if err != nil || string(got) != "payload" {
			t.Fatalf("deep: contents not copied: %v %q", err, got)
		}
	})

	t.Run("shallow", func(t *testing.T) {
		t.Parallel()
		src, link := newSrc(t)
		dst := filepath.Join(t.TempDir(), "out")
		opts := archive.CopyOptions{OnSymlink: archive.SymlinkShallow}
		if err := archive.CopyDir(t.Context(), src, dst, opts); err != nil {
			t.Fatalf("CopyDir: %v", err)
		}
		dstLink := filepath.Join(dst, "link")
		info, err := os.Lstat(dstLink)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Fatal("shallow: dst entry is not a symlink")
		}
		orig, err := os.Readlink(link)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.Readlink(dstLink)
		if err != nil || got != orig {
			t.Fatalf("shallow: got link target %q want %q (err=%v)", got, orig, err)
		}
	})

	t.Run("ignore", func(t *testing.T) {
		t.Parallel()
		src, _ := newSrc(t)
		dst := filepath.Join(t.TempDir(), "out")
		opts := archive.CopyOptions{OnSymlink: archive.SymlinkIgnore}
		if err := archive.CopyDir(t.Context(), src, dst, opts); err != nil {
			t.Fatalf("CopyDir: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(dst, "link")); !os.IsNotExist(err) {
			t.Fatalf("ignore: expected no entry, got err=%v", err)
		}
	})
}

func TestCopyDir_SelfCopyRejection(t *testing.T) {
	t.Parallel()

	t.Run("sameDir", func(t *testing.T) {
		t.Parallel()
		src := t.TempDir()
		err := archive.CopyDir(t.Context(), src, src, archive.CopyOptions{})
		if !errors.Is(err, archive.ErrSelfCopy) {
			t.Fatalf("got %v, want ErrSelfCopy", err)
		}
	})

	t.Run("nestedDst", func(t *testing.T) {
		t.Parallel()
		src := t.TempDir()
		dst := filepath.Join(src, "nested", "copy")
		err := archive.CopyDir(t.Context(), src, dst, archive.CopyOptions{})
		if !errors.Is(err, archive.ErrSelfCopy) {
			t.Fatalf("got %v, want ErrSelfCopy", err)
		}
	})

	t.Run("srcNotDirectory", func(t *testing.T) {
		t.Parallel()
		src := writeFile(t, t.TempDir(), "f.txt", "x", 0o640)
		err := archive.CopyDir(t.Context(), src, filepath.Join(t.TempDir(), "out"), archive.CopyOptions{})
		if err == nil {
			t.Fatal("expected error copying a non-directory src")
		}
	})

	t.Run("siblingIsFine", func(t *testing.T) {
		t.Parallel()
		parent := t.TempDir()
		src := filepath.Join(parent, "src")
		writeFile(t, src, "f.txt", "x", 0o640)
		dst := filepath.Join(parent, "dst")
		if err := archive.CopyDir(t.Context(), src, dst, archive.CopyOptions{}); err != nil {
			t.Fatalf("sibling destination must be allowed: %v", err)
		}
	})
}

func TestCopyDir_CancelledContext(t *testing.T) {
	t.Parallel()

	t.Run("cancelledBeforeStart", func(t *testing.T) {
		t.Parallel()
		src := t.TempDir()
		writeFile(t, src, "a.txt", "x", 0o640)
		dst := filepath.Join(t.TempDir(), "out")

		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		err := archive.CopyDir(ctx, src, dst, archive.CopyOptions{})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
		if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
			t.Fatal("cancelled-before-start copy must not create dst")
		}
	})

	t.Run("cancelledMidCopy", func(t *testing.T) {
		t.Parallel()
		src := t.TempDir()
		for i := range 20 {
			writeFile(t, src, filepath.Join("f", strconv.Itoa(i)+".txt"), "x", 0o640)
		}
		dst := filepath.Join(t.TempDir(), "out")

		ctx, cancel := context.WithCancel(t.Context())
		seen := 0
		opts := archive.CopyOptions{
			Skip: func(string, os.FileInfo) (bool, error) {
				seen++
				if seen == 3 {
					cancel()
				}
				return false, nil
			},
		}
		err := archive.CopyDir(ctx, src, dst, opts)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
		got := relTree(t, dst)
		if len(got) >= 20 {
			t.Fatalf("expected the copy to stop early, got %d entries", len(got))
		}
	})
}

func TestCopyDir_MaxEntriesExceeded(t *testing.T) {
	t.Parallel()
	src := t.TempDir()
	writeFile(t, src, "a.txt", "x", 0o640)
	writeFile(t, src, "b.txt", "x", 0o640)
	writeFile(t, src, "c.txt", "x", 0o640)
	dst := filepath.Join(t.TempDir(), "out")

	opts := archive.CopyOptions{MaxEntries: 2}
	err := archive.CopyDir(t.Context(), src, dst, opts)
	if !errors.Is(err, archive.ErrTooManyEntries) {
		t.Fatalf("got %v, want ErrTooManyEntries", err)
	}
}
