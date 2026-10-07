<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — internal/snapshot

Bounded, type-preserving graph copies for mutable values.

## Contract

- Call `snapshot.Clone`; do not add local deep-copy variants.
- Keep graph cycles and concrete scalar, map, slice, pointer, array, struct, and
  `time.Time` types.
- Reject channels, functions, unsafe pointers, mutable unexported fields, and
  maps with non-string keys.
- Keep 65,536-node work bound. Use iterative traversal; no recursion.
