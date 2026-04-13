# Agent guide — httpx/ws

Thin wrapper over coder/websocket. Provides a same-origin-by-default upgrade + a reference in-process broadcaster.

## Conventions

- `ws.Accept(w, r, opts)` enforces origin check before the upgrade. Empty `AllowedOrigins` = same-origin (r.Host) only. `"*"` disables the check — use only for genuinely public APIs.
- The returned `*websocket.Conn` is the upstream type, so all coder/websocket methods (Read, Write, Close, Ping) are available directly.
- `Broadcaster[T]` is single-process fan-out. When fan-out must cross replicas, wire a pubsub backend (realtime/pubsub, Step 10).
- Slow subscribers get messages dropped (best-effort delivery). Apps that need guaranteed delivery should queue via river (Step 7).

## Don't

- Don't roll your own upgrader. `ws.Accept` keeps origin checks + framework defaults in one place.
- Don't share a single `*websocket.Conn` across goroutines for writes without synchronization. Reads are single-consumer only.
