<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — internal/robustfs

Rename and remove boundary shared by filesystem-backed packages.

## Contract

- Use `robustfs.Rename` / `robustfs.Remove` when another handle holds
  target open (readers, scanners, AV).
- Windows: retry only `ERROR_ACCESS_DENIED` and `ERROR_SHARING_VIOLATION`
  (Go `os.Open` omits `FILE_SHARE_DELETE`); bounded 64 attempts, about 1.9s.
- Unix: no retry; first error returned.
- Non-transient errors returned unwrapped; callers wrap with own prefix and
  keep `errors.Is(err, os.ErrNotExist)` checks.
- Keep platform behavior in build-tagged files. Run Windows tests under Wine;
  cross-compilation alone proves no runtime boundary.
