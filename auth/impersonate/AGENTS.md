<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# auth/impersonate

Admin-as-user session projection. Audit hooks. Terminal exit flow.

## Surface

- `Middleware(opts)` -> request `Principal{Current, Original}`.
- `Begin(w, r, opts, targetUserID)` -> start; nesting forbidden.
- `ExitHandler(opts)` -> POST-only terminal endpoint; success `204`.
- `X-Impersonating` -> active target ID for UI banner.
- `QueryParamExit` -> deprecated compatibility symbol; no state change.

## Invariants

- Exit route behind `httpx/csrf` or equivalent CSRF middleware.
- Exit request never reaches downstream business handler after actor restore.
- `SessionGet` plus `SessionSet` backed by application session store.
- `OnImpersonate` plus `OnExit` wired to `audit/`.
- Mutation requires relevant audit hook; audit failure blocks session change.
- Audit hooks receive request context and return persistence errors.
- `Begin` only after application authorization check.
