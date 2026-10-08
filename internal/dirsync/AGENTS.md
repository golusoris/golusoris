<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — internal/dirsync

Directory-entry durability boundary shared by filesystem-backed packages.

## Contract

- Pass opened directory handle to `dirsync.Sync` after durable file writes
  and namespace changes.
- Unix: propagate every directory `fsync` failure.
- Windows: attempt `FlushFileBuffers`; ignore only documented unsupported
  read-only-directory handle errors. Propagate capacity and I/O failures.
- Keep platform behavior in build-tagged files. Run Windows tests under Wine;
  cross-compilation alone does not prove runtime boundary.
