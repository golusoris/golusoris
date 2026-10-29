<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# ADR-0023: Keep hand-written OAuth2 authorization server

- **Status**: Accepted
- **Date**: 2026-10-28
- **Deciders**: @lusoris
- **Tags**: `auth`, `security`, `dependencies`

## Context

The package `auth/oauth2server` provides a minimal OAuth 2.0 authorization server implementing the authorization code grant with mandatory PKCE for first-party applications ([`auth/oauth2server/oauth2server.go:5-16`](https://github.com/golusoris/golusoris/blob/main/auth/oauth2server/oauth2server.go#L5-L16)). It intentionally omits OpenID Connect (OIDC), ID tokens, discovery, UserInfo, and refresh tokens. The implementation comprises 730 lines of Go backed by 1153 lines of unit tests across [`auth/oauth2server/*_test.go`](https://github.com/golusoris/golusoris/tree/main/auth/oauth2server).

Key surface and invariants re-verified against [`auth/oauth2server/oauth2server.go`](https://github.com/golusoris/golusoris/blob/main/auth/oauth2server/oauth2server.go):

- **Core types and stores**: `Client` (:56), `AuthRequest` (:67), `Code` (:79), atomic delete-on-read `CodeStore` (:85), `ClientStore` (:91), `Options` (:96), and `Server` (:112). Single-replica in-memory implementations: `MemoryClientStore` (:564) and `MemoryCodeStore` (:704).
- **Constructors and routing**: `New` (:115) and `Server.Routes` (:155) exposing GET `/authorize` (:162) and POST `/token` (:268).
- **Grants and PKCE**: Restricts grants strictly to `authorization_code` (:170, :280). Mandates RFC 7636 PKCE `S256` (:218-230, :442-449) with 43–128 character RFC 7636 unreserved charset (:53), rejecting missing challenges and `plain` methods.
- **Client authentication**: Confidential clients authenticate via `client_secret_post` using `subtle.ConstantTimeCompare` (:350-363); public clients bypass secrets (:356).
- **Token issuance**: Injected `*jwt.Signer` signs bearer JWTs with registered claims and granted scopes (:380-405).
- **Input bounds**: 8 KiB form body limit (:48), 1 KiB / 64-token scope limits (:49-51, :467-490), 60s default code TTL (:146), and 1h access token TTL (:140).
- **Redirect URI validation**: Exact matching on registered URIs with RFC 8252 loopback IP port variance for public clients (:493-561) and reverse-domain private-use URI schemes (:663-667).
- **Atomic code redemption**: Single-call `CodeStore.Take` (:87, :330) guarantees single-use redemption and eliminates double-spend race windows.

## Considered options

| Option | Licence | Latest Tag / Date | Maintenance Status | Transitive Deps | Evaluation |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `github.com/go-oauth2/oauth2/v4` | MIT | v4.6.0 (2026-09-01) | Low; 117 open issues | 52 edges / 49 modules | Rejects: `manage/util.go:28` `DefaultValidateURI` uses `strings.HasSuffix(redirect.Host, base.Host)` (open redirect hazard); `manage/manager.go:236/248` uses non-atomic `GetByCode` then `RemoveByCode` (race hazard); requires ~450 LOC in adapters (7-method `TokenStore`, `ClientInfo`, `AccessGenerate`) and would replace the public `ClientStore`/`CodeStore` contracts (HISS-14 append-only ABI). |
| `github.com/ory/fosite` | Apache-2.0 | v0.49.0 (2024-12-12) | Slow/dormant; 61 open issues | 3352 edges / 464 modules | Rejects: 4 published security advisories (GHSA-grfp-q2mm-hfp6, GHSA-rfq3-w54c-f9q5, GHSA-7mqr-2v3q-v2wm, GHSA-v3q9-2p3m-7g43); pulls 464 modules (gRPC, OTEL, Viper); 20+ method `Storage` interface; wraps token minting. |
| `github.com/zitadel/oidc/v3/pkg/op` | Apache-2.0 | v3.51.16 (2026-10-15) | Active; 51 open issues | 30 edges / 27 modules | Rejects: full OIDC provider only (demands JWKS, discovery, userinfo, ID tokens); violates minimal non-OIDC scope. 1 advisory (GHSA-j8gq-92xf-382c). |
| `github.com/openshift/osin` | BSD-3-Clause | v1.0.1 (2017-09-05) | Abandoned; 0 releases since 2017 | 2 edges / 0 modules | Rejects: allows insecure `plain` PKCE challenge method; legacy storage API lacks `context.Context` propagation. |
| Keep hand-written | EUPL-1.2 | In-tree (`auth/oauth2server`) | Active (maintainer) | 0 external modules | Chosen: 730 LOC + 1153 LOC tests (`auth/oauth2server/*_test.go`), zero external dependencies; atomic `CodeStore.Take` eliminates redemption races; strict PKCE S256. |

Adopting third-party engines would incur disproportionate maintenance and integration costs:

- `go-oauth2/oauth2/v4` would require ~450 LOC in adapter shims across a 7-method `TokenStore`, `ClientInfo`, and `AccessGenerate` and would replace the public `ClientStore`/`CodeStore` contracts (HISS-14 append-only ABI), while introducing 49 transitive dependencies. Its single-domain `GetDomain()` model breaks RFC 8252 multi-redirect validation, and its split `GetByCode`/`RemoveByCode` design creates code double-spend races.
- `ory/fosite` would pull 464 transitive dependencies into the build graph and force implementation of a 20+ method storage interface for features this framework explicitly excludes.
- `zitadel/oidc/v3/pkg/op` couples authorization to full OpenID Connect discovery, userinfo, and key management endpoints.
- `openshift/osin` is unmaintained since 2017 and permits `plain` PKCE transformations.

## Decision

Keep the hand-written OAuth2 authorization server in `auth/oauth2server`.

The existing 730 LOC implementation fulfills all first-party application requirements with zero external dependencies and fits behind clean stores (`ClientStore`, `CodeStore`) and `auth/jwt.Signer`. Third-party engines either introduce transitive dependencies (up to 464 modules), impose unneeded protocol surface (full OIDC), or introduce architectural security hazards (`strings.HasSuffix` open redirect matching and non-atomic code redemption).

## Consequences

- **Positive**: Zero third-party dependency additions across the module tree (saves 27 to 464 modules).
- **Positive**: Avoids the suffix-matching redirect check and split code redemption found in `go-oauth2/oauth2/v4`.
- **Positive**: Preserves native integration with `auth/jwt.Signer` without adapter boilerplate.
- **Negative**: Maintainers own ongoing security fixes, RFC updates, and vulnerability monitoring for the authorization server implementation.
- **Known gaps**: Follow-up issue #1195 tracks RFC 9207 `iss` parameter support on authorization response redirects ([`auth/oauth2server/oauth2server.go:251-266`](https://github.com/golusoris/golusoris/blob/main/auth/oauth2server/oauth2server.go#L251-L266)) and RFC 6749 §2.3.1 / RFC 9700 §4.3.2 `client_secret_basic` HTTP Authorization header support.
- **Revisit trigger**: If requirements expand to require refresh token rotation, token revocation (RFC 7009), token introspection (RFC 7662), or a full OpenID Connect provider suite (discovery, JWKS, UserInfo, ID tokens), re-evaluate `github.com/zitadel/oidc/v3/pkg/op`.

## References

- Issue #1175 (OAuth2 Server Engine Evaluation).
- Follow-up issue #1195 (OAuth2 BCP hardening: RFC 9207 `iss`, `client_secret_basic`).
- RFC 6749: The OAuth 2.0 Authorization Framework.
- RFC 7636: Proof Key for Code Exchange by OAuth Public Clients (PKCE).
- RFC 8252: OAuth 2.0 for Native Apps (BCP 212).
- RFC 9207: OAuth 2.0 Authorization Server Issuer Identification.
- RFC 9700: Best Current Practice for OAuth 2.0 Security (BCP 195).
