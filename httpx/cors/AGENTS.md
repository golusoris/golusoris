<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — httpx/cors

CORS middleware wrapping rs/cors. Denies cross-origin by default.

## Conventions

- Explicit allowlist: `http.cors.origins` required.
- `New`: returns middleware plus validation error.
- Credentials plus exact `*` origin -> rejected. Partial host wildcard remains explicit policy.
- Mount near top of middleware stack so preflight OPTIONS short-circuit before auth + rate limit.

## Don't

- Don't discard `New` validation errors.
