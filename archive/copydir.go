// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package archive

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	dircopy "github.com/otiai10/copy"
)

// SymlinkPolicy controls how [CopyDir] treats symbolic links found in src.
type SymlinkPolicy int

const (
	// SymlinkShallow recreates the symlink itself at dst (default).
	SymlinkShallow SymlinkPolicy = iota
	// SymlinkDeep copies the link target's contents instead of the link.
	SymlinkDeep
	// SymlinkIgnore skips symlinks entirely; nothing is written for them.
	SymlinkIgnore
)

// DefaultMaxEntries bounds a [CopyDir] call when [CopyOptions.MaxEntries] is
// zero (HISS-02: every loop needs a scalar upper bound).
const DefaultMaxEntries = 200_000

// ErrTooManyEntries is returned when a copy visits more than its entry budget.
var ErrTooManyEntries = errors.New("archive: too many entries")

// ErrSelfCopy is returned when dst is src itself, or lies inside src.
var ErrSelfCopy = errors.New("archive: destination is src or nested inside it")

// CopyOptions tunes [CopyDir].
type CopyOptions struct {
	// OnSymlink selects how symlinks under src are handled. The zero value is
	// SymlinkShallow.
	OnSymlink SymlinkPolicy

	// PreservePermissions copies each entry's exact source file mode onto its
	// copy, including a source directory that lacks the owner-write bit (for
	// example a vendored or extracted read-only tree). A directory is always
	// created writable first and chmoded to its final mode only once its
	// contents are copied, so a read-only source directory does not block
	// its own population.
	//
	// When false (the default), CopyDir does not mirror the source mode at
	// all: every directory is created at the ordinary umask-masked "new
	// directory" mode (as plain os.MkdirAll(dest, 0o777) would give it) and
	// every file keeps whatever mode os.Create gave it, regardless of the
	// source entry's mode. This guarantees the copy is always usable — the
	// caller can read and write into it — even when src contains read-only
	// files or directories; it deliberately does not replicate a restrictive
	// source mode onto the copy.
	PreservePermissions bool

	// Skip is called once for every entry under src — never for src itself —
	// with the entry's source path and its os.FileInfo. Returning true omits
	// the entry, and its entire subtree when it is a directory, from the
	// copy. A nil Skip omits nothing.
	Skip func(path string, info os.FileInfo) (bool, error)

	// MaxEntries bounds the number of entries CopyDir will visit (HISS-02),
	// including ones Skip goes on to exclude. Zero uses DefaultMaxEntries;
	// exceeding it aborts the copy with ErrTooManyEntries.
	MaxEntries int
}

// CopyDir recursively copies the directory tree rooted at src to dst,
// creating dst as needed, via github.com/otiai10/copy. It refuses to copy a
// directory into itself and checks ctx for cancellation before starting and
// again before each entry, so a cancelled ctx stops the copy between entries
// rather than only at the next disk error.
//
// CopyDir does not roll back on failure: a cancelled ctx, an exceeded
// MaxEntries, a Skip error, or an I/O error (for example a permission
// failure) all stop the copy where it stands and return a non-nil error,
// leaving whatever was already written in place under dst. Callers that need
// an all-or-nothing copy should copy into a fresh temporary directory next
// to dst and rename it into place only once CopyDir returns nil.
func CopyDir(ctx context.Context, src, dst string, opts CopyOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("archive: stat %s: %w", src, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("archive: %s is not a directory", src)
	}
	if err := rejectSelfCopy(src, dst); err != nil {
		return err
	}

	if err := dircopy.Copy(src, dst, buildCopyOptions(ctx, opts)); err != nil {
		return fmt.Errorf("archive: copy %s to %s: %w", src, dst, err)
	}
	return nil
}

// buildCopyOptions adapts a [CopyOptions] into the otiai10/copy Options that
// implement it: the policy mappings plus one Skip hook that folds in ctx
// cancellation and the HISS-02 entry bound ahead of the caller's own Skip.
func buildCopyOptions(ctx context.Context, opts CopyOptions) dircopy.Options {
	maxEntries := opts.MaxEntries
	if maxEntries <= 0 {
		maxEntries = DefaultMaxEntries
	}
	visited := 0

	return dircopy.Options{
		OnSymlink:         func(string) dircopy.SymlinkAction { return symlinkAction(opts.OnSymlink) },
		PermissionControl: permissionControl(opts.PreservePermissions),
		Skip: func(entryInfo os.FileInfo, entrySrc, _ string) (bool, error) {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			visited++
			if visited > maxEntries {
				return false, fmt.Errorf("%w: more than %d", ErrTooManyEntries, maxEntries)
			}
			if opts.Skip == nil {
				return false, nil
			}
			return opts.Skip(entrySrc, entryInfo)
		},
	}
}

// symlinkAction maps a SymlinkPolicy onto the otiai10/copy action it selects.
func symlinkAction(p SymlinkPolicy) dircopy.SymlinkAction {
	switch p {
	case SymlinkDeep:
		return dircopy.Deep
	case SymlinkIgnore:
		return dircopy.Skip
	case SymlinkShallow:
		return dircopy.Shallow
	default:
		return dircopy.Shallow
	}
}

// permissionControl maps PreservePermissions onto the otiai10/copy hook that
// implements it.
func permissionControl(preserve bool) dircopy.PermissionControlFunc {
	if preserve {
		return dircopy.PerservePermission
	}
	return nonPreservingPermissionControl
}

// nonPreservingPermissionControl implements PreservePermissions=false. It
// deliberately does not use the library's own dircopy.DoNothing: DoNothing
// creates each destination directory via os.MkdirAll(dest, srcinfo.Mode()),
// which mirrors the *source* directory's mode onto dest immediately, before
// any of its contents are copied. When a source directory itself lacks the
// owner-write bit (an extracted archive, a vendored tree, or anything else
// checked out read-only — a routine input, not an edge case), that leaves
// the freshly created destination directory just as unwritable, so copying
// the very entries it is about to receive fails with a permission error
// partway through, aborting the whole copy with part of the tree already on
// disk. Preservation is what PreservePermissions is for; the non-preserving
// default must instead give every directory the same ordinary,
// umask-masked "new directory" mode a plain os.MkdirAll(dest, 0o777) would
// (independent of the source's mode), so the copy always succeeds and the
// result is always usable. Files are left untouched, as DoNothing already
// does: os.Create in the library's fcopy gives every new file the process's
// standard umask-masked mode regardless of the source file's mode.
var nonPreservingPermissionControl dircopy.PermissionControlFunc = func(srcinfo fs.FileInfo, dest string) (func(*error), error) {
	if srcinfo.IsDir() {
		if err := os.MkdirAll(dest, 0o777); err != nil {
			return func(*error) {}, fmt.Errorf("archive: mkdir %s: %w", dest, err)
		}
	}
	return func(*error) {}, nil
}

// rejectSelfCopy errors if dst is src, or lies inside src — copying a
// directory into itself would otherwise grow without bound. The check is
// lexical (Abs + Clean, no symlink resolution): a symlinked ancestor that
// aliases src and dst is not caught, matching the caveat already documented
// for Extract in AGENTS.md.
func rejectSelfCopy(src, dst string) error {
	absSrc, err := filepath.Abs(src)
	if err != nil {
		return fmt.Errorf("archive: resolve %s: %w", src, err)
	}
	absDst, err := filepath.Abs(dst)
	if err != nil {
		return fmt.Errorf("archive: resolve %s: %w", dst, err)
	}
	if isInside(filepath.Clean(absSrc), filepath.Clean(absDst)) {
		return ErrSelfCopy
	}
	return nil
}

// isInside reports whether target is base or lies inside base. Both must
// already be absolute and clean.
func isInside(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
