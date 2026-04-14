// Package archive provides extraction and creation of compressed archives
// (zip, tar.gz, tar.bz2, tar.xz, tar.zst, 7z, rar) via mholt/archives.
//
// Picks: mholt/archives v0.1.5 — the only Go library handling all common
// archive formats in one API. No CGO required for zip/tar/gz/bz2/xz/zst.
// RAR and 7z read-only (nwaples/rardecode, bodgit/sevenzip — both pure Go).
//
// Usage:
//
//	err := archive.Extract(ctx, "backup.tar.gz", "/var/restore")
//	err = archive.Create(ctx, "bundle.zip", []string{"/var/www"})
package archive

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/mholt/archives"
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

		f, openErr := fsys.Open(path)
		if openErr != nil {
			return fmt.Errorf("archive: open entry %s: %w", path, openErr)
		}
		defer f.Close() //nolint:errcheck

		out, createErr := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
		if createErr != nil {
			return fmt.Errorf("archive: create %s: %w", dest, createErr)
		}
		defer out.Close() //nolint:errcheck

		if _, copyErr := io.Copy(out, f); copyErr != nil {
			return fmt.Errorf("archive: extract %s: %w", path, copyErr)
		}
		return nil
	})
}

// Create builds an archive at dest whose format is inferred from the
// extension (e.g. ".zip", ".tar.gz"). srcs is a list of files or directories.
func Create(ctx context.Context, dest string, srcs []string) error {
	files, err := archives.FilesFromDisk(ctx, nil, srcsMap(srcs))
	if err != nil {
		return fmt.Errorf("archive: collect files: %w", err)
	}

	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("archive: create %s: %w", dest, err)
	}
	defer out.Close() //nolint:errcheck

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
