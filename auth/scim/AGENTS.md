<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# auth/scim

SCIM 2.0 subset. RFC 7643 plus RFC 7644. User and Group provisioning.

## Surface

- `Handler(store)` -> default `/Users`, `/Users/{id}`, `/Groups`, `/Groups/{id}` handler.
- `HandlerWithOptions(store, ...option)` -> explicit logger or body limit.
- Mount prefix: `/scim/v2/`.
- `WithLogger(logger)` -> internal backend error evidence.
- `WithMaxRequestBodyBytes(n)` -> JSON body limit; default 1 MiB.
- `Store` -> application persistence, typically sqlc plus PostgreSQL.
- `ErrNotFound` -> HTTP `404`.

## Invariants

- One bounded JSON value only. Trailing value rejected.
- Input `schemas` must contain matching core User or Group URI.
- Backend failure -> internal log plus generic external `500`; no backend
  detail leak.
- Authentication at caller route; bearer middleware required.
- PATCH absent. PUT replaces resource.
- Filter string opaque to handler; Store owns RFC 7644 expression
  parsing.
- `count=0` or negative returns no resources plus `totalResults`; page size
  defaults to 100, caps at 1000, and handler caps over-returning stores.
- Response encoding failures after committed headers go to configured logger.
