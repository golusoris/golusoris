<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# ai/tiny/internal/trainerio

Shared unexported trainer I/O boundary.

## Contract

- Dataset input: canonical absolute root; tenant segment; regular local file.
- Staging: one verified open source identity; fixed read-only destination.
- Config: encoded JSON capped at 1 MiB before either staged write.
- Gemma JSONL: record, example, aggregate-text caps.
- LiteRT archives: traversal, link, special-file, member, expansion caps.
- Output: fixed filename; regular node; byte cap; private snapshot outside
  runner-writable output.
- Upload: hash, key, bytes from one immutable snapshot; post-upload digest check.
- Keys: escaped tenant, model, job, digest segments; no caller path fragments.

## Change gate

- Identity-sensitive path change: replacement-node and symlink negative tests.
- Bound change: exact-limit and limit-plus-one tests.
- Run: `go test -race -count=1 ./ai/tiny/internal/trainerio`.
