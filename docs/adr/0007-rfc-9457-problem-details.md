# ADR-0007: RFC 9457 Problem Details for HTTP error bodies

- **Status**: Accepted
- **Date**: 2026-04-13 (backfill — Step 3c)
- **Deciders**: @lusoris
- **Tags**: http, api, errors

## Context

HTTP error bodies in Go services typically take one of three shapes:

1. Bare string: `"invalid input"`.
2. Ad-hoc JSON: `{"code": "VALIDATION_ERROR", "message": "..."}`.
3. [RFC 9457 Problem Details for HTTP APIs](https://www.rfc-editor.org/rfc/rfc9457): `application/problem+json` with `type`, `title`, `status`, `detail`, `instance`, plus extension fields.

Apps in [principles.md §2.5](../principles.md) compliance scope (NIS2, GDPR, BSI C5) are increasingly required to emit machine-readable error envelopes for incident analysis and audit logs. Custom envelopes mean every client (web, mobile, partner, internal) must implement parsing per-app.

## Decision

We will emit `application/problem+json` (RFC 9457) for every HTTP error response in framework-provided handlers. `ogenkit.ErrorHandler` is the single source of truth — generated ogen handlers funnel errors through it; framework middleware (`httpx/middleware/Recover`) emits the same shape on panics.

The framework's `errors/` package maps `gerr.Code` → `(status, type-URI, title)` so app code only deals with `gerr.Wrap(err, gerr.CodeNotFound)`.

## Alternatives considered

| Option | Pros | Cons | Why not chosen |
|---|---|---|---|
| Ad-hoc `{code, message}` | Simple, familiar | Every client re-implements parsing; no standard for extension fields | Standardising on a custom shape costs more than adopting RFC 9457. |
| GraphQL-style error array | Works for batch responses | RFC 9457 already covers single-error case; doesn't apply to REST | Wrong protocol model. |
| Bare HTTP status codes | Minimal | Loses structured detail (which field failed validation, etc.) | Insufficient for compliance audit logs. |

## Consequences

- **Positive**: Standard `application/problem+json` parses with off-the-shelf libraries in every language. `type` URI gives an extensible namespace for app-specific error codes (`https://golusoris.dev/errors/validation`). Audit logs can index by `type` for trend analysis.
- **Negative**: Slightly more verbose than `{code, message}`. Apps migrating from existing services must update their clients (one-time cost, documented in migration notes).
- **Follow-ups**: Document the framework's standard `type` URIs in [`docs/architecture/error-codes.md`](../architecture/error-codes.md) (TBD). Add a `gerr → ProblemDetails` table to `errors/AGENTS.md`.

## References

- [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457) — Problem Details for HTTP APIs (supersedes RFC 7807).
- `ogenkit.ErrorHandler` — implementation.
- [principles.md §2.6](../principles.md) — adopted wire-protocol standards.
