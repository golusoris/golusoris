<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — errors/

> Security-relevant: messages on these errors can surface in API responses.

golusoris's error package — thin layer over
[go-faster/errors](https://github.com/go-faster/errors) adding typed `Code`
plus HTTP-status mapping that ogenkit and HTTP middleware understand. All
domain errors should be built here so wire status + RFC 9457 problem body
are consistent.

## Key API

| Symbol | Purpose |
|---|---|
| `New(code, msg)` | construct coded `*Error` |
| `Wrap(err, code, msg)` | attach code+message to cause (nil-in → nil-out) |
| `NotFound/BadRequest/Unauthorized/Forbidden/Conflict/Validation/Internal/RateLimited(msg)` | sugar constructors |
| `Code` + `Code*` consts | stable machine-readable classes (`not_found`, …) |
| `Code.Status()` / `Error.Status()` | HTTP status mapping |
| `ProblemFromError(err, fallbackStatus, instance)` | build canonical RFC 9457 body; sanitize all 5xx detail |
| `WriteProblem(w, problem)` | write shared `application/problem+json` response |
| `Is` / `As` / `Unwrap` / `Errorf` | re-exports of go-faster/errors |

`*Error` implements `Unwrap`, so `errors.Is` / `errors.As` traverse to cause as usual.

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

- **Don't leak internals to clients.** `ProblemFromError` sanitizes every 5xx
  response, but callers and logs still see `Message` and `Cause`. Keep secrets
  out of both and use generic message for unexpected failures.
- **Use standard codes** — don't extend `Code` const set in framework
 code; define app-specific codes in app code if truly needed. Unknown codes
 map to 500.
- Wrap, don't swallow: pass lower-level error as `Cause` so `errors.Is`
 still works for caller.

## Don't

- Don't put secrets, tokens, SQL, or raw driver strings in `Message`.
- Don't return bare stdlib error from handler path — wrap it so it carries
 `Code` (otherwise it maps to 500 with no useful body).
- Don't compare errors by string; use `errors.Is` / `errors.As`.
- Don't import both this package and go-faster/errors in same file —  re-exports cover common helpers.
