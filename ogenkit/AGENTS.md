<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — ogenkit

Adapters so ogen-generated servers fit golusoris conventions.

## Usage

```go
srv, err := api.NewServer(handler,
    api.WithErrorHandler(ogenkit.ErrorHandler(logger)),
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
- Ogen errors retain status classification and emit `about:blank` Problem
  Details; legacy `error_message` remains as extension field.
- `SlogMiddleware` logs per ogen operation; it's separate from outer `httpx/middleware.Logger` which logs per HTTP request.
- `RecoverMiddleware` converts panics inside ogen handlers into `gerr.Internal`; outer HTTP `Recover` middleware is final safety net.

## Don't

- Don't return raw Go errors from handlers. Wrap via `gerr.Wrap` or convenience
  constructors. Raw errors become generic 500 Problem Details without internal
  detail.
- Don't layer `httpx/middleware.Recover` inside ogen middleware chain — use ogenkit variant there so log has operation ID.
