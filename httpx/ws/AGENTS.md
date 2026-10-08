<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — httpx/ws

Thin wrapper over coder/websocket. Provides same-origin-by-default upgrade + reference in-process broadcaster.

## Conventions

- `ws.Accept(w, r, opts)` enforces exact scheme, host, and effective-port origin
  checks before upgrade. Empty `AllowedOrigins` means same-origin only; entries
  are exact `http(s)://host[:port]` origins. `"*"` disables the check—public
  APIs only.
- TLS terminator: run `middleware.TrustProxy` before `ws.Accept`. One valid
  trusted `X-Forwarded-Proto` value supplies the external scheme. Raw,
  duplicate, chained, or untrusted values never affect origin checks.
- returned `*websocket.Conn` is upstream type, so all coder/websocket methods (Read, Write, Close, Ping) are available directly.
- `Broadcaster[T]` is single-process fan-out. When fan-out must cross replicas, wire `realtime/pubsub` backend.
- Slow subscribers get messages dropped (best-effort delivery). Apps that need guaranteed delivery should queue via river.

## Don't

- Don't roll your own upgrader. `ws.Accept` keeps origin checks + framework defaults in one place.
- Don't share single `*websocket.Conn` across goroutines for writes without synchronization. Reads are single-consumer only.
