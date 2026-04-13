# Agent guide — archive/

Multi-format archive extraction and creation via mholt/archives.

## Supported formats

zip · tar · tar.gz · tar.bz2 · tar.xz · tar.zst · 7z (read) · rar (read)

Format is inferred from the file extension.

## API

```go
// Extract archive into directory:
err := archive.Extract(ctx, "backup.tar.gz", "/var/restore")

// Create archive from files/directories:
err = archive.Create(ctx, "bundle.zip", []string{"/var/www", "/etc/app"})
```

## Security

mholt/archives strips leading `/` and `../` path components automatically,
preventing zip-slip attacks. The `Extract` implementation also MkdirAlls
with 0o750 permissions.

## Don't

- Don't pass user-controlled destination paths to `Extract` without
  validating they are inside the expected base directory.
- Don't use `Create` with RAR or 7z extensions — they are read-only formats.
