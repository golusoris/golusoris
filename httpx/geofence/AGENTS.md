<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — httpx/geofence

Country allow/deny middleware backed by MaxMind mmdb file.

## Conventions

- Apps supply mmdb path via `http.geofence.mmdb`. framework does not bundle GeoLite2 (MaxMind licensing + ~4MB binary asset).
- Codes are ISO-3166-1 alpha-2 (`US`, `DE`, `KP`, …). Case-insensitive.
- Policy: `Allow` wins if non-empty. Else `Deny` blocks. Both normalized lists empty -> no-op; MMDB stays unopened.
- Peer IP is from `r.RemoteAddr`. Run `middleware.TrustProxy` first when behind proxy.

## Don't

- Don't treat geofence as a compliance control. It's coarse filter — VPN / Tor exits defeat it by design. Use it to reduce attack surface, not to enforce export restrictions.
