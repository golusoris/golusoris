# Agent guide — httpx/htmx

Stateless helpers for HTMX request detection + response headers.

## Conventions

- `htmx.IsRequest(r)` to branch server-side rendering (partial vs full page).
- Response helpers (`PushURL`, `Redirect`, `Trigger`, `Reswap`, `Retarget`, `Refresh`) set the matching `HX-*` response header.
- Header name constants are exported so apps + middleware can reference them without hard-coding strings.

## Don't

- Don't mix `htmx.Redirect` with `http.Redirect` — HTMX intercepts navigation and uses `HX-Redirect`. Mixing them breaks the swap.
