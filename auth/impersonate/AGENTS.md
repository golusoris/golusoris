# auth/impersonate

Audited admin-as-user impersonation with a one-click revert.

## Surface

- `impersonate.Middleware(opts)` — injects `Principal{Current, Original}` into the request context.
- `impersonate.Begin(w, r, opts, targetUserID)` — start (no nesting allowed).
- Header `X-Impersonating` is set on every response while impersonating; UI renders a banner.
- Query `?exit_impersonation=1` reverts via `SessionSet`.

## Notes

- App must wire `SessionGet` / `SessionSet` to its session store.
- App must wire `OnImpersonate` / `OnExit` to its audit log (`audit/`).
- Authorization (which admins may impersonate) is the app's responsibility — call `Begin` only after the policy check.
