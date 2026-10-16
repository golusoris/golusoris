<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — container/registry/sign/signtest/

- API: `NewRegistryImage(t)` -> bounded context, client, seeded image digest.
- API: `RoundTrip(t, signer, foreignKey)` -> sign, verify, reject foreign key.
- Foreign-key generation and failure cases: provider-specific.
