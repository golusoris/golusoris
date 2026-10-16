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

## Conventions

- Handler code returns `*gerr.Error` via helpers like `gerr.NotFound`.
  `ErrorHandler` emits RFC 9457 `application/problem+json`; legacy
  `{code, message}` remains as extension fields.
- `ErrorHandlerWith` maps sentinel errors via `WithStatus` and validation field errors to 422 `invalid-params` via `WithInvalidParams`.
- Ogen errors retain status classification and emit `about:blank` Problem
  Details; legacy `error_message` remains as extension field.
- `SlogMiddleware` logs per ogen operation; separate from outer `httpx/middleware.Logger` logging per HTTP request.
- `RecoverMiddleware` converts panics inside ogen handlers into `gerr.Internal`; outer HTTP `Recover` middleware provides final safety net.

## Banned

- Return raw Go errors from handlers forbidden; wrap via `gerr.Wrap` or convenience constructors so generic 500 without internal detail avoided.
- Layer `httpx/middleware.Recover` inside ogen middleware chain forbidden; use ogenkit variant preserving operation ID.
