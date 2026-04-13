# Agent guide — ogenkit

Adapters so ogen-generated servers fit the golusoris conventions.

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

- Handler code returns `*gerr.Error` via helpers like `gerr.NotFound`. ogenkit's `ErrorHandler` maps these to the right HTTP status + a JSON body `{code, message}`.
- ogen's own errors (DecodeRequestError, SecurityError, ParameterError) fall through to ogen's DefaultErrorHandler so ogen retains its own 4xx classifications.
- `SlogMiddleware` logs per ogen operation; it's separate from the outer `httpx/middleware.Logger` which logs per HTTP request.
- `RecoverMiddleware` converts panics inside ogen handlers into `gerr.Internal`; the outer HTTP `Recover` middleware is a final safety net.

## Don't

- Don't return raw Go errors from handlers — wrap via `gerr.Wrap` or one of the convenience constructors. Raw errors go through ogen's default handler and show up as generic 500.
- Don't layer `httpx/middleware.Recover` inside the ogen middleware chain — use the ogenkit variant there so the log has the operation ID.
