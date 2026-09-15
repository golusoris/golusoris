<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — archive/

Multi-format archive extraction and creation via mholt/archives, plus a
recursive directory copy via otiai10/copy.

## Supported formats

zip · tar · tar.gz · tar.bz2 · tar.xz · tar.zst · 7z (read) · rar (read)

Format is inferred from the file extension.

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
preventing zip-slip attacks. The `Extract` implementation also MkdirAlls
with 0o750 permissions.

`CopyDir` rejects a dst equal to or nested inside src (`ErrSelfCopy`) via a
lexical `filepath.Abs`+`Clean` comparison — it does not resolve symlinks, so
a symlinked ancestor that aliases src and dst is not caught. `CopyDir` also
bounds the number of entries it will visit (`CopyOptions.MaxEntries`,
default `DefaultMaxEntries`; HISS-02) and checks `ctx` for cancellation
before starting and once per entry.

`CopyDir` does not roll back on failure: a cancelled `ctx`, an exceeded
`MaxEntries`, a `Skip` error, or an I/O error all stop the copy where it
stands and return a non-nil error, leaving whatever was already written in
place under dst rather than silently discarding it or silently succeeding.
Copy into a fresh temporary directory and rename it into place if an
all-or-nothing copy is required.

`CopyOptions.PreservePermissions` (default `false`) controls whose mode ends
up on the copy, not just whether it does:

- `false` (default): every directory is created at the ordinary
  umask-masked "new directory" mode, and every file keeps whatever mode
  `os.Create` gave it — neither is derived from the source entry's mode at
  all. This is deliberate: a source directory that itself lacks the
  owner-write bit (an extracted archive, a vendored tree, anything checked
  out read-only) must not make the copy destination unwritable, or copying
  its own contents into it fails with a permission error partway through.
  The default always produces a usable copy the caller can read and write.
- `true`: each entry's exact source mode is copied onto its copy. A
  directory is still created writable first and chmoded to its final
  (possibly read-only) mode only after its contents are copied, so a
  read-only source directory does not block its own population either way
  — but the *result* is then read-only if the source was, by design.

## Don't

- Don't pass user-controlled destination paths to `Extract` without
  validating they are inside the expected base directory.
- Don't use `Create` with RAR or 7z extensions — they are read-only formats.
- Don't rely on `CopyDir`'s self-copy check to defend against a symlinked
  dst — validate user-controlled paths the same way you would for `Extract`.
