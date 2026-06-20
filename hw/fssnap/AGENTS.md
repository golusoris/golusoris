# Agent guide — hw/fssnap/

ZFS and Btrfs snapshot helpers that shell out to the `zfs` / `btrfs` CLI tools.
Stateless utility — **no fx wiring**. Own go.mod sub-module; import directly:
`github.com/golusoris/golusoris/hw/fssnap`.

## API

```go
// ZFS (var fssnap.ZFS)
fssnap.ZFS.Snapshot(ctx, dataset, tag)     // creates dataset@tag
snaps, err := fssnap.ZFS.List(ctx, dataset)
fssnap.ZFS.Destroy(ctx, "dataset@tag")
fssnap.ZFS.Rollback(ctx, "dataset@tag")

// Btrfs (var fssnap.Btrfs)
fssnap.Btrfs.Snapshot(ctx, src, dst)       // read-only snapshot of src at dst
snaps, err := fssnap.Btrfs.List(ctx, subvolume)
fssnap.Btrfs.Delete(ctx, path)
```

`ZFS` and `Btrfs` are zero-value singletons — no constructor.

## Notes

- Linux-specific; no meaning on other platforms (hence the separate go.mod).
- Pure stdlib at runtime (`os/exec`); testify is a test-only dep.
- Requires the `zfs` / `btrfs` binaries on `PATH` and the privileges to run them
  (typically root or `CAP_SYS_ADMIN`). Errors wrap combined stdout+stderr.
- Caller-supplied `dataset`/`tag`/`path` are passed as exec args (not a shell),
  so no shell-injection — but still validate them; they reach a privileged tool.
