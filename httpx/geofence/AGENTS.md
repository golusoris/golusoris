# Agent guide — httpx/geofence

Country allow/deny middleware backed by a MaxMind mmdb file.

## Conventions

- Apps supply the mmdb path via `http.geofence.mmdb`. The framework does not bundle GeoLite2 (MaxMind licensing + ~4MB binary asset).
- Codes are ISO-3166-1 alpha-2 (`US`, `DE`, `KP`, …). Case-insensitive.
- Policy: `Allow` wins if non-empty (allowlist). Otherwise `Deny` acts as a blocklist. Both empty → no-op.
- Peer IP is from `r.RemoteAddr`. Run `middleware.TrustProxy` first when behind a proxy.

## Don't

- Don't treat geofence as a compliance control. It's a coarse filter — VPN / Tor exits defeat it by design. Use it to reduce attack surface, not to enforce export restrictions.
