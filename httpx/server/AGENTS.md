# Agent guide — httpx/server

Wraps `*http.Server` with slow-loris guards, body limits, graceful shutdown.

## Conventions

- Requires `http.Handler` in the fx graph (from `httpx/router` or an ogen handler chain).
- All timeouts are opinionated non-zero defaults to protect against slow-loris + resource exhaustion. Apps tune via `http.timeouts.*` / `http.limits.*`.
- `Limits.Body == 0` disables the body cap (use for streaming endpoints that intentionally accept unbounded input — rare).
- Uses `net.Listen` explicitly so `:0` picks a free port for tests; `srv.Serve` runs in a goroutine so fx Start isn't blocked.
