<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — hw/fssnap/

ZFS and Btrfs snapshot helpers that shell out to `zfs` / `btrfs` CLI tools.
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

- Linux-specific; no meaning on other platforms (hence separate go.mod).
- Pure stdlib at runtime (`os/exec`); testify is test-only dep.
- Requires `zfs` / `btrfs` binaries on `PATH` and privileges to run them
 (typically root or `CAP_SYS_ADMIN`). Errors wrap combined stdout+stderr.
- Reject empty operands, control characters, and values starting with `-` before
  invoking a privileged tool. Do not weaken this option-injection boundary.
- ZFS destructive calls accept exactly one `dataset@tag`; never a dataset,
  bookmark, range, or list.
- Btrfs list output uses ` path ` as its field boundary. Preserve complete
  suffix because snapshot paths may contain spaces.
