<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — internal/riverclientstate

River client settings read without calling methods that panic.

## Contract

- `WorksJobs(c)`: true only when `Config.Queues` non-empty. False for nil,
  zero client, unknown River layout.
- `LeaderElectionDisabled(c)`: true for nil, zero client, unknown layout, or
  `Config.LeaderElectionDisabled`.
- Reads unexported `config` field via `reflect`. River `PeriodicJobs()` and
  `Subscribe()` panic on these settings.
- Never probe those methods under `recover()`: recover also catches runtime
  faults, which corrupt heap on Windows hosts with large exception contexts
  (golang/go#81238). Gate: `make fault-recovery`.
- River bump: `TestWorkingClient` green required; red means field renamed,
  callers then reject every client.
