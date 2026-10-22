<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# auth/oidc

OIDC plus OAuth 2.0 PKCE client. `coreos/go-oidc/v3`. Bounded discovery.

## Flow

1. `Begin(ctx, state)` -> `AuthRequest{URL, Verifier, Nonce}`; store verifier plus nonce in server session.
2. Callback state validation.
3. `Complete(ctx, code, req)` -> verified `TokenSet` (nonce validated).
4. `UserInfo(ctx, accessToken)` -> claims.
Legacy/eager alternative: `AuthURL(state)` plus `Exchange(ctx, code, verifier)`.

## Session flow

- `NewSessionFlow(provider, SessionFlowOptions{Sessions, Clock, MaxAge, OnSuccess, OnError})` -> `Login` + `Callback` handlers (`http.HandlerFunc` shape).
- `Login`: random state + `Begin`; state, nonce, verifier, issued-at -> `auth/session`; 302 to IdP.
- `Callback`: flow keys deleted + saved before checks (single use, replay fails); IdP `error` -> `ErrFlowIDP` (code quoted, max 64 bytes); state mismatch or missing -> `ErrFlowState`; age > `MaxAge` (default 10m) -> `ErrFlowExpired`; no code -> `ErrFlowCode`; `Complete` sentinels pass through; success -> `Session.Renew` then `OnSuccess(w, r, sess, toks)`.
- `OnSuccess` stores identity, saves, responds. Claim authorization (allowed users, groups) stays caller-side.

## Config

- `auth.oidc.issuer_url`
- `auth.oidc.client_id`
- `auth.oidc.client_secret`
- `auth.oidc.redirect_url`
- `auth.oidc.scopes`; default `openid,email,profile`
- `auth.oidc.discovery_timeout`; default `10s`
- `auth.oidc.lazy_discovery`; default `false`
- `auth.oidc.disable_offline_access`; default `false`

## Errors

- `ErrExchange` -> token exchange failure with IdP.
- `ErrIDTokenMissing` -> response lacks `id_token`.
- `ErrIDTokenInvalid` -> ID token verification failure (`gerr.Unauthorized` wrapped).
- `ErrNonceMismatch` -> empty or mismatched nonce (`gerr.Unauthorized` wrapped).

## Construction

- `Module` -> config-backed fx provider.
- `NewProvider(ctx, opts, logger)` -> caller cancellation plus bounded discovery.
- `Options.LazyDiscovery` -> discovery deferred to first `Begin`/`Complete`/`Exchange`/`UserInfo`.
- `Options.HTTPClient` -> injected transport; copied and timeout-bounded when needed.
- Same client -> discovery, token exchange, JWKS, UserInfo.

## Tokens & Claims

- `TokenSet.Subject`, `TokenSet.Nonce` -> populated from verified ID token.
- `TokenSet.Claims(v)` -> unmarshals verified ID token claims.
- `Complete` compares nonces via `crypto/subtle.ConstantTimeCompare`.

## PKCE

S256 always on. Verifier stored in `auth/session`; never client cookie payload.
Custom scopes copied, always include `openid`.

## Invariants

- Nonempty state and callback match mandatory.
- Raw ID token server-side only.
- UserInfo cached via `cache/memory` or `auth/session`; no per-request fetch.
- Zero-timeout outbound client forbidden.
