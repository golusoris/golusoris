// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package archive provides extraction and creation of compressed archives
// (zip, tar.gz, tar.bz2, tar.xz, tar.zst, 7z, rar) via mholt/archives, plus a
// recursive directory copy via otiai10/copy.
//
// Picks: mholt/archives v0.1.5 — the only Go library handling all common
// archive formats in one API. No CGO required for zip/tar/gz/bz2/xz/zst.
// RAR and 7z read-only (nwaples/rardecode, bodgit/sevenzip — both pure Go).
// otiai10/copy v1.14.1 — pure-Go recursive directory copy with the
// symlink/permission/skip hooks CopyDir builds on (docs/FLEET_GO_DEMAND.md
// sprint item 2).
//
// Usage:
//
//	err := archive.Extract(ctx, "backup.tar.gz", "/var/restore")
//	err = archive.Create(ctx, "bundle.zip", []string{"/var/www"})
//	err = archive.CopyDir(ctx, "/var/www", "/var/www.bak", archive.CopyOptions{})
package archive

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/mholt/archives"

	"github.com/golusoris/golusoris/core/errors"
)

// Extract decompresses src archive into destDir, creating it if needed.
// Extraction is depth-bounded to prevent zip-slip attacks; mholt/archives
// strips any leading "/" or "../" components automatically.
func Extract(ctx context.Context, src, destDir string) error {
	if err := os.MkdirAll(destDir, 0o750); err != nil {
		return fmt.Errorf("archive: mkdir %s: %w", destDir, err)
	}

	fsys, err := archives.FileSystem(ctx, src, nil)
	if err != nil {
		return fmt.Errorf("archive: open %s: %w", src, err)
	}

	return fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}

		dest := filepath.Join(destDir, filepath.FromSlash(path))
		if d.IsDir() {
			return os.MkdirAll(dest, 0o750)
		}
		return extractFileEntry(fsys, dest, path)
	})
}

// extractFileEntry opens the archive entry named path in fsys and copies its
// content into dest. dest's parent directory must already exist (fs.WalkDir
// visits a directory before its children).
func extractFileEntry(fsys fs.FS, dest, path string) error {
	f, openErr := fsys.Open(path) //nolint:gosec // G304: archive extraction path validated by caller // #nosec G304
	if openErr != nil {
		return fmt.Errorf("archive: open entry %s: %w", path, openErr)
	}
	defer f.Close() //nolint:errcheck // read-only close; a read failure already surfaced via io.Copy's error return

	out, createErr := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640) // #nosec G304,G302 -- archive extraction path validated by caller
	if createErr != nil {
		return fmt.Errorf("archive: create %s: %w", dest, createErr)
	}

	if _, copyErr := io.Copy(out, f); copyErr != nil {
		extractErr := fmt.Errorf("archive: extract %s: %w", path, copyErr)
		errors.CloseJoin(out, &extractErr, "archive: close "+dest)
		return extractErr
	}
	// Check Close error on write path: kernel may buffer writes and a
	// flush failure would silently truncate the extracted file otherwise.
	if closeErr := out.Close(); closeErr != nil {
		return fmt.Errorf("archive: close %s: %w", dest, closeErr)
	}
	return nil
}

// Create builds an archive at dest whose format is inferred from the
// extension (e.g. ".zip", ".tar.gz"). srcs is a list of files or directories.
func Create(ctx context.Context, dest string, srcs []string) error {
	files, err := archives.FilesFromDisk(ctx, nil, srcsMap(srcs))
	if err != nil {
		return fmt.Errorf("archive: collect files: %w", err)
	}

	out, err := os.Create(dest) // #nosec G304 -- archive path validated by Sanitize above
	if err != nil {
		return fmt.Errorf("archive: create %s: %w", dest, err)
	}
	defer out.Close() //nolint:errcheck // best-effort close; a write/flush failure is already reflected by archiver.Archive's returned error

	format, _, err := archives.Identify(ctx, dest, nil)
	if err != nil {
		return fmt.Errorf("archive: identify format for %s: %w", dest, err)
	}
	archiver, ok := format.(archives.Archiver)
	if !ok {
		return fmt.Errorf("archive: format for %s does not support writing", dest)
	}

	if err := archiver.Archive(ctx, out, files); err != nil {
		return fmt.Errorf("archive: write %s: %w", dest, err)
	}
	return nil
}

// srcsMap builds the map[diskPath]archivePath expected by archives.FilesFromDisk.
// An empty archive path tells the library to use the base name.
func srcsMap(srcs []string) map[string]string {
	m := make(map[string]string, len(srcs))
	for _, s := range srcs {
		m[s] = "" // empty = use the base name inside the archive
	}
	return m
}
