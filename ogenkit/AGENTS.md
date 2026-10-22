<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — ogenkit

Adapters so ogen-generated servers fit golusoris conventions.

## Usage

```go
srv, err := api.NewServer(handler,
    api.WithErrorHandler(ogenkit.ErrorHandlerWith(logger,
        ogenkit.WithInvalidParams(),
        ogenkit.WithStatus(domain.ErrNotFound, gerr.CodeNotFound),
    )),
    api.WithMiddleware(
        ogenkit.SlogMiddleware(logger),
        ogenkit.RecoverMiddleware(logger),
    ),
)
```

```go
// ogen Handler.NewError(ctx, err):
resolver := ogenkit.NewResolver(
    ogenkit.WithInvalidParams(),
    ogenkit.WithStatus(domain.ErrNotFound, gerr.CodeNotFound),
)

func (h *Handler) NewError(_ context.Context, err error) *api.ErrorStatusCode {
    p := resolver.Resolve(nil, err)
    return &api.ErrorStatusCode{StatusCode: p.Status, Response: toAPIProblem(p)}
}
```

## Conventions

- Handler code returns `*gerr.Error` via helpers like `gerr.NotFound`.
  `ErrorHandler` emits RFC 9457 `application/problem+json`; legacy
  `{code, message}` remains as extension fields.
- `ErrorHandlerWith` maps sentinel errors via `WithStatus` or `WithPublicStatus` (unredacted 5xx chain detail, deliberate caller opt-in), validation field errors to 422 `invalid-params` via `WithInvalidParams`. Problem responses set `Cache-Control: no-store`.
- `NewResolver(opts...)` / `Resolver.Resolve(r, err)`: shares mapping table with `ErrorHandlerWith`; `r` nil sets `instance` empty; for `Handler.NewError`.
- `WithFallbackDetail(public)`: unmapped 5xx errors -> fixed public detail + message; empty public ignored.
- `WithValidationType(uri)`: validation problem type URI override with `WithInvalidParams`; empty ignored.
- `WithSecurityStatus(status, public)`: `*ogenerrors.SecurityError` -> fixed status + public detail (empty = status text); status outside 400..599 ignored.
- `WithHTTPStatus(target, status, public)`: sentinel -> any 400..599 status no `gerr.Code` carries (502, 422); fixed public detail; empty public = full chain message (opt-in). Nil target or status outside 400..599 ignored. First match wins across all mapping options.
- `WithPathInstance`: problem `instance` = URL path; query never echoed. Default = `RequestURI()`.
- `WithDecodeDetail(public)`: ogen request/params decode errors -> fixed detail, ogen status kept; raw decoder text never sent. `WithInvalidParams` still wins for `validate.Error`.
- Ogen errors retain status classification and emit `about:blank` Problem
  Details; legacy `error_message` remains as extension field.
- `SlogMiddleware` logs per ogen operation; separate from outer `httpx/middleware.Logger` logging per HTTP request.
- `RecoverMiddleware` converts panics inside ogen handlers into `gerr.Internal`; outer HTTP `Recover` middleware provides final safety net.

## Banned

- Return raw Go errors from handlers forbidden; wrap via `gerr.Wrap` or convenience constructors so generic 500 without internal detail avoided.
- Layer `httpx/middleware.Recover` inside ogen middleware chain forbidden; use ogenkit variant preserving operation ID.
