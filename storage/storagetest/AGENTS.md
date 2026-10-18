<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — storage/storagetest/

- API: `CheckPagedListing(ctx, t, b)` -> verifies paged listing and boundary semantics on `storage.Bucket`.
- Conformance: seeds `MaxListLimit+1` keys under `walk/` plus `walk-other/x`, verifies `storage.Walk` byte order, checks `storage.ListOptions.StartAfter` bounds, confirms invalid key rejection.
