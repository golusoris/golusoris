# ADR-0004: ogen over oapi-codegen for OpenAPI

- **Status**: Accepted
- **Date**: 2026-04-13 (backfill)
- **Deciders**: @lusoris
- **Tags**: http, api, codegen

## Context

The framework targets API-first development: write `openapi.yaml`, generate types + server + client. Two main Go generators:

- **[ogen-go/ogen](https://github.com/ogen-go/ogen)** — code-generated, typed-everything, zero-reflection, OpenAPI 3.1 native.
- **[deepmap/oapi-codegen](https://github.com/oapi-codegen/oapi-codegen)** — older, more popular, supports many server adapters (chi, gin, echo, …), but uses `interface{}` in places.

Per [PLAN.md §2.6](../../.workingdir/PLAN.md), OpenAPI 3.1 + JSON Schema 2020-12 are pinned standards.

## Decision

We will use `github.com/ogen-go/ogen` for generating server stubs, request/response types, and clients from `openapi.yaml`. The framework provides `ogenkit/` with shared middleware (RFC 9457 error handler, slog middleware, recover middleware) that ogen-generated handlers compose with.

## Alternatives considered

| Option | Pros | Cons | Why not chosen |
|---|---|---|---|
| `oapi-codegen` | Most popular, multi-router | OpenAPI 3.0 first (3.1 partial); uses `interface{}` for `oneOf` / `anyOf`; slower codegen | Type erasure on `oneOf` is exactly what we don't want for safety. |
| Hand-written types + `chi` routes | No codegen | API drift; clients out of sync; OpenAPI spec becomes documentation only | Defeats the API-first contract. |
| `gnostic` + custom generator | Full control | Maintenance burden of a custom generator | Not our differentiator. |

## Consequences

- **Positive**: Generated code is fully typed including `oneOf`/`anyOf`/`allOf` discriminators. ogen's reflectionless decoder is fast and amenable to fuzzing per [PLAN.md §2.8](../../.workingdir/PLAN.md). RFC 9457 Problem Details body emitted by `ogenkit.ErrorHandler` (see [ADR-0007](0007-rfc-9457-problem-details.md)).
- **Negative**: Smaller community than oapi-codegen — fewer Stack Overflow answers. ogen lags slightly on newer 3.1 features (e.g. webhook syntax). Codegen takes ~3-5s on large specs.
- **Follow-ups**: `apidocs/` ships Scalar UI + MCP JSON-RPC for the same `openapi.yaml`, keeping spec → docs → tools in sync.

## References

- ogen pinned at v1.20.3 — see [`docs/upstream/ogen/`](../upstream/ogen/).
- ogenkit middleware: [`ogenkit/`](../../ogenkit/).
- [ADR-0007](0007-rfc-9457-problem-details.md) — error body format.
