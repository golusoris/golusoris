# auth/scim

Minimal SCIM 2.0 (RFC 7643 + 7644) HTTP handlers for User and Group provisioning.

## Surface

- `scim.Handler(store)` → `http.Handler` for `/Users`, `/Users/{id}`, `/Groups`, `/Groups/{id}` (mount under `/scim/v2/`).
- `scim.Store` interface — apps implement persistence (typically Postgres via sqlc).
- Types: `User`, `Group`, `Email`, `Name`, `Member`, `ListResponse`, `Error`.
- `scim.ErrNotFound` — return from Store to map to HTTP 404.

## Notes

- Authentication is the caller's responsibility; wrap the handler in a bearer-token middleware.
- PATCH is not implemented yet; PUT replaces the resource.
- Filter strings are passed through to the Store opaquely; Store may parse RFC 7644 §3.4.2.2 expressions (e.g. `userName eq "alice"`).
