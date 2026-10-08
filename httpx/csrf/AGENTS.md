<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# httpx/csrf

Browser same-origin CSRF enforcement via `filippo.io/csrf/gorilla`.

## Contract

- `http.csrf.secret` empty -> no-op middleware.
- Nonempty secret must decode to 32 bytes; compatibility enable switch only. Pinned dependency ignores key value.
- Unsafe cross-site browser requests -> 403 via `Sec-Fetch-Site` / `Origin` checks.
- Requests without browser origin metadata -> allowed as non-browser traffic.
- `Token` returns compatibility random text; token, form field, and `X-CSRF-Token` never authorize requests.
- `Secure`, `Domain`, `Path` = ignored compatibility fields; no CSRF cookie exists.

Bearer-only API routes -> separate sub-router outside middleware.
