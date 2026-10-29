<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# auth/oauth2server

Minimal OAuth authorization server: authorization-code flow with PKCE.
Not an OIDC issuer; no ID tokens, discovery, UserInfo, or nonce handling.

## Surface

- `oauth2server.New(opts)` → `(*Server, error)`; errors when `Issuer`, `Clients`, `Codes`, `Signer` or `Authenticate` is unset.
- `Server.Routes()` → `http.Handler` exposing `/authorize` + `/token`.
- `MemoryClientStore`, `MemoryCodeStore` for tests / single-replica deployments.
  `MemoryClientStore.Register` reports validation failures; legacy `Add`
  rejects invalid clients without storing them.
- `Options.Authenticate(r)` is integration point with your session store.

## Notes

- Engine: hand-written; ADR-0023. Grant: authorization_code + PKCE S256.
- Client authentication: client_secret_basic, client_secret_post; public clients: none.
- Basic credentials: RFC 6749 §2.3.1 / Appendix B form decoding; constant-time secret comparison.
- Mixed Basic + form credentials -> invalid_request; failed Basic -> 401 invalid_client + WWW-Authenticate: Basic.
- Authorization success/error redirects: RFC 9207 iss = Options.Issuer; url.Values encoding; state preserved.
- Unauthenticated user (Authenticate returns empty) -> 401 login required; no redirect.
- Invalid client/redirect URI: direct error; no redirect. Evidence: issuer_basic_test.go.
- Only `authorization_code` grant is supported; PKCE `S256` is mandatory.
- `/authorize` accepts GET only; `/token` accepts POST only.
- PKCE challenges and verifiers must be 43-128 RFC 7636 unreserved characters.
- Scope token and SP separators follow RFC 6749 ASCII grammar. Requested set:
  deduplicated, bounded, subset of registered `Client.Scopes`.
- Token exchange: revalidates code scope against current client allowlist.
- Access JWT: carries granted scope.
- Access tokens are JWTs signed by injected `*jwt.Signer`. ID-token issuance and refresh tokens are intentionally not implemented yet.
- Token success and error responses emit `Cache-Control: no-store` plus
  `Pragma: no-cache`.
- Client registration validates identity, redirects, scopes, and confidential
  secrets; memory storage clones policy slices.
- Public native clients accept RFC 8252 reverse-domain private schemes,
  claimed HTTPS redirects, and loopback IP redirects with runtime ports.
- Codes are single-use and TTL-bounded (default 60s).
- Mount under trusted issuer URL; no rate limiting; combine with `httpx/ratelimit/`.
