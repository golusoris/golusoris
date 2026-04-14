# auth/oauth2server

Minimal OAuth 2.1 / OIDC issuer: authorization-code flow with PKCE.

## Surface

- `oauth2server.New(opts)` → `*Server`.
- `Server.Routes()` → `http.Handler` exposing `/authorize` + `/token`.
- `MemoryClientStore`, `MemoryCodeStore` for tests / single-replica deployments.
- `Options.Authenticate(r)` is the integration point with your session store.

## Notes

- Only `authorization_code` grant is supported; PKCE (`S256` or `plain`) is mandatory.
- Access tokens are JWTs signed by the injected `*jwt.Signer`. ID-token issuance and refresh tokens are intentionally not implemented yet.
- Codes are single-use and TTL-bounded (default 60s).
- Mount under your trusted issuer URL — there is no rate limiting; combine with `httpx/ratelimit/`.
