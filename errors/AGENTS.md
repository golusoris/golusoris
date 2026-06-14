# Agent guide — errors/

> Security-relevant: messages on these errors can surface in API responses.

golusoris's error package — a thin layer over
[go-faster/errors](https://github.com/go-faster/errors) adding a typed `Code`
plus HTTP-status mapping that ogenkit and HTTP middleware understand. All
domain errors should be built here so the wire status + RFC 9457 problem body
are consistent.

## Key API

| Symbol | Purpose |
|---|---|
| `New(code, msg)` | construct a coded `*Error` |
| `Wrap(err, code, msg)` | attach code+message to a cause (nil-in → nil-out) |
| `NotFound/BadRequest/Unauthorized/Forbidden/Conflict/Validation/Internal/RateLimited(msg)` | sugar constructors |
| `Code` + `Code*` consts | stable machine-readable classes (`not_found`, …) |
| `Code.Status()` / `Error.Status()` | HTTP status mapping |
| `Is` / `As` / `Unwrap` / `Errorf` | re-exports of go-faster/errors |

`*Error` implements `Unwrap`, so `errors.Is` / `errors.As` traverse to the
cause as usual.

## Usage

```go
if u == nil {
    return errors.NotFound("user not found")          // → 404, code not_found
}
if err := db.Query(ctx); err != nil {
    return errors.Wrap(err, errors.CodeUnavailable, "postgres unreachable") // → 503
}
```

## Usage caveats

- **Don't leak internals to clients.** The `Message` is rendered into the
  response body. Keep it human-safe; put stack/driver detail in the wrapped
  `Cause` (logged server-side), not the message. Prefer `CodeInternal` with a
  generic message for unexpected failures.
- **Use the standard codes** — don't extend the `Code` const set in framework
  code; define app-specific codes in app code if truly needed. Unknown codes
  map to 500.
- Wrap, don't swallow: pass the lower-level error as the `Cause` so `errors.Is`
  still works for the caller.

## Don't

- Don't put secrets, tokens, SQL, or raw driver strings in `Message`.
- Don't return a bare stdlib error from a handler path — wrap it so it carries
  a `Code` (otherwise it maps to 500 with no useful body).
- Don't compare errors by string; use `errors.Is` / `errors.As`.
- Don't import both this package and go-faster/errors in the same file — the
  re-exports cover the common helpers.
