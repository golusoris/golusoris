# Agent guide — observability/statuspage

Public `/status` endpoint rendering the check registry + uptime as HTML or JSON.

## Conventions

- The same `Registry` powers `/livez` / `/readyz` (k8s/health, Step 6). Register each check once — share the registry across endpoints.
- Every check has a 2s per-call timeout. Design check functions to fail fast; long checks block the whole render.
- Format negotiation: `Accept: application/json` or `?format=json` → JSON. Otherwise HTML. JSON response is 503 on overall down so uptime probes can use the same endpoint.

## Don't

- Don't register checks that hit downstream APIs on every request. Run them on a ticker + write results into the registry's cache, then serve `Cached()` from the handler.
- Don't put sensitive detail in `err.Error()` of a check — the message is surfaced on the public page.
