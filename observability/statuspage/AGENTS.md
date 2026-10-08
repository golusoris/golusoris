<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — observability/statuspage

Public `/status` endpoint rendering check registry + uptime as HTML or JSON.

## Conventions

- same `Registry` powers `/livez` / `/readyz` through `k8s/health`. Register each check once — share registry across endpoints.
- Every check has 2s per-call timeout. Design check functions to fail fast; long checks block whole render.
- Format negotiation: `Accept: application/json` or `?format=json` → JSON. Otherwise HTML. JSON response is 503 on overall down so uptime probes can use same endpoint.
- `Details` must be safely cloneable and JSON-serializable. Registry preserves
  concrete types across separate provider, hook, caller, and cache snapshots.

## Don't

- Don't register checks that hit downstream APIs on every request. Run them on schedule, write results into registry cache, then serve `Cached()` from handler.
- Don't put sensitive detail in `err.Error()` of check — message is surfaced on public page.
