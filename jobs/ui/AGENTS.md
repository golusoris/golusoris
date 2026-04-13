# Agent guide — jobs/ui

River UI dashboard. Apps mount it at an admin path with basic-auth.

## Conventions

- NEVER mount on the public router. UI exposes raw job args + retry
  controls — an attacker with access can replay/discard jobs.
- Basic-auth via `ui.WithBasicAuth(handler, user, pass)` — constant-time
  comparison. Empty creds = no auth (only safe on localhost / behind a
  VPN / on an admin-only sub-router with its own auth middleware).
- `HideJobArgs` defaults args to hidden in the list view. Enable for
  apps whose JobArgs carry PII.

## Don't

- Don't mount without calling `Start(ctx, handler)` — the UI's caches
  need background initialization. [Module] (future addition) will
  handle the lifecycle; for now callers plumb it manually.
