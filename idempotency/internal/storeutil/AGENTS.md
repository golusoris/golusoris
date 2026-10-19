<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — idempotency/internal/storeutil/

Shared store argument checks; typed-nil detection lives in `core/validate/nilcheck`.

- Standard library only; no store import cycle.
- Context cancellation first; nonempty key / fingerprint / token; positive TTL.
- Sweep cap supplied by caller from `store.MaxSweepBatch`.
