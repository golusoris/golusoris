<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# auth/linking

External `(provider, subject)` identity -> local user ID.

## Surface

- `New(store)` -> `(*Service, error)`; nil dependencies rejected.
- `Link(...)` -> atomic claim; same-owner idempotence; cross-owner conflict.
- `Lookup(...)` -> local user ID.
- `List(...)` -> identities for user.
- `Unlink(ctx, userID, provider, subject)` -> atomic owner-checked removal.
- `MemoryStore` -> race-safe test store.

## Store contract

- `Store.Claim` = single atomic owner decision; never overwrite owner.
- `Store.DeleteOwned` = single atomic owner check plus delete.
- Unique key: `(provider, subject)`.
- Nonblank provider, subject, and user ID bytes remain exact; never trim or normalize opaque identity keys.
- Claim result = authoritative stored identity, whether inserted or existing.
- PostgreSQL: conflict-safe insert plus authoritative return in one statement
  or transaction.
- `MemoryStore.Save` deprecated compatibility helper; owner overwrite forbidden.
