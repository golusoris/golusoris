<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — testutil/dragonfly/

- Real Dragonfly via GenericContainer; rueidis client; Docker required.
- Image: internal/testimages.Dragonfly; explicit Options.Image immutable tag+digest only.
- Start/StartWith: connected client; Addr/AddrWith: host:port; automatic cleanup.
- Container path: short skip; provider health; startgate; pinned reaper; bounded start/stop.
- Cmd: `--proactor_threads=2`; port 6379/tcp.
- Tracking: OPTIN/OPTOUT/NOLOOP supported; BCAST/PREFIX/REDIRECT unavailable; ADR-0028.
- Isolation: fresh server per test; no shared parallel container.
- Validation tests: pure Options.image; invalidation regression: tracked reader plus uncached writer.
