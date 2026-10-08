<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# auth/oidc

OIDC plus OAuth 2.0 PKCE client. `coreos/go-oidc/v3`. Bounded discovery.

## Flow

1. `AuthURL(state)` -> URL plus verifier; verifier into server-side session.
2. Callback state validation.
3. `Exchange(ctx, code, verifier)` -> verified token set.
4. `UserInfo(ctx, accessToken)` -> claims.

## Config

- `auth.oidc.issuer_url`
- `auth.oidc.client_id`
- `auth.oidc.client_secret`
- `auth.oidc.redirect_url`
- `auth.oidc.scopes`; default `openid,email,profile`
- `auth.oidc.discovery_timeout`; default `10s`

## Construction

- `Module` -> config-backed fx provider.
- `NewProvider(ctx, opts, logger)` -> caller cancellation plus bounded discovery.
- `Options.HTTPClient` -> injected transport; copied and timeout-bounded when needed.
- Same client -> discovery, token exchange, JWKS, UserInfo.

## PKCE

S256 always on. Verifier stored in `auth/session`; never client cookie payload.
Custom scopes are copied and always include `openid`.

## Invariants

- Nonempty state and callback match mandatory.
- Raw ID token server-side only.
- UserInfo cached via `cache/memory` or `auth/session`; no per-request fetch.
- Zero-timeout outbound client forbidden.
