<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — internal/testimages/

Immutable OCI authority for testcontainers helpers + opt-in integration tests.

## Contract

- Constants: `tag@sha256:<64 lowercase hex>`.
- `Validate`: reject mutable caller overrides before Docker access.
- `WithPinnedReaper`: override testcontainers Ryuk default.
- `.github/testcontainers-images.txt`: same default set except opt-in ClamAV
  + split-module emulators (`FakeGCSServer`); Module sweep job pulls those.
- Renovate: update tag + digest copies together; policy test prevents drift.
