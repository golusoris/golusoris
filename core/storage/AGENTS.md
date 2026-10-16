<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — core/storage/

Backend-neutral object storage contracts. Key validation; bounded listing; upload-presign validation. Backend implementations -> separate modules.

## Key surface

| Symbol | Purpose |
| --- | --- |
| `storage.Bucket` | Concurrent backend contract |
| `storage.CleanKey` | Canonical safe key validation |
| `storage.NormalizeListOptions`, `storage.Walk` | Bounded ordered listing |
| `storage.PutPresigner`, `storage.ValidatePresignPut` | Upload signing contract and shared validation |

## Rules

- No imports from root module or backend implementations.
- Keep validation and behavior shared across backends.
- Key errors preserve `storage:` text and `ErrUnsafeKey` identity.
