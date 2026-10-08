<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — archive/

Multi-format archive extraction and creation via mholt/archives, plus recursive directory copy via otiai10/copy.

## Supported formats

zip · tar · tar.gz · tar.bz2 · tar.xz · tar.zst · 7z (read) · rar (read)

Format is inferred from file extension.

## API

```go
// Extract archive into directory:
err := archive.Extract(ctx, "backup.tar.gz", "/var/restore")

// Create archive from files/directories:
err = archive.Create(ctx, "bundle.zip", []string{"/var/www", "/etc/app"})

// Recursively copy a directory tree:
err = archive.CopyDir(ctx, "/var/www", "/var/www.bak", archive.CopyOptions{
    PreservePermissions: true,
})
```

## Security

mholt/archives strips leading `/` and `../` path components automatically,
preventing zip-slip attacks. `Extract` implementation also MkdirAlls
with 0o750 permissions.

`CopyDir` rejects dst equal to or nested inside src (`ErrSelfCopy`) via lexical `filepath.Abs`+`Clean` comparison — it does not resolve symlinks, so
symlinked ancestor that aliases src and dst is not caught. `CopyDir` also
bounds number of entries it will visit (`CopyOptions.MaxEntries`,
default `DefaultMaxEntries`; HISS-02) and checks `ctx` for cancellation
before starting and once per entry.

`CopyDir` does not roll back on failure. Cancelled `ctx`, exceeded
`MaxEntries`, `Skip` error, or I/O error stops copy where it stands and
returns non-nil error. Content already written under dst remains.
Copy into fresh temporary directory and rename it into place if all-or-nothing copy is required.

`CopyOptions.PreservePermissions` (default `false`) controls whose mode ends
up on copy, not whether it does:

- `false` (default): directories use ordinary umask-masked new-directory
 mode. Files keep modes assigned by `os.Create`; neither mode derives from
 source entry. This prevents read-only source directories from making copy
 destinations unwritable during population. Default output remains readable
 and writable by caller.
- `true`: each copy receives its source entry's exact mode. Directories start
 writable and receive final mode after content copy. Read-only source
 directories cannot block population, but results remain read-only by design.

## Don't

- Don't apply read-only source directory mode before copying its contents; it
 can make destination unwritable.
- Don't pass user-controlled destination paths to `Extract` without
 validating they are inside expected base directory.
- Don't use `Create` with RAR or 7z extensions — they are read-only formats.
- Don't rely on `CopyDir`'s self-copy check to defend against symlinked
 dst — validate user-controlled paths same way you would for `Extract`.
