<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — jobs/ui

River UI dashboard. Apps mount it at admin path with basic-auth.

## Conventions

- NEVER mount on public router. UI exposes raw job args + retry
 controls — attacker with access can replay/discard jobs.
- `Options.Prefix` must name a non-root admin path. Empty and `/` prefixes
 fail construction.
- Basic-auth via `ui.WithBasicAuth(handler, user, pass)` — constant-time
 comparison. Empty creds = no auth (only safe on localhost / behind VPN / on admin-only sub-router with its own auth middleware). One missing credential = fail closed.
- Nil or typed-nil handlers fail closed with HTTP 500. `Start` rejects nil
 context and handler dependencies.
- `HideJobArgs` defaults args to hidden in list view. Enable for
 apps whose JobArgs carry PII.

## Don't

- Don't mount without calling `Start(ctx, handler)` — UI caches
 need background initialization. Callers plumb lifecycle manually.
- Don't put credentials in `Options`; retained fields fail construction.
 Wrap mounted handler with `WithBasicAuth`.
