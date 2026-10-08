<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

Generate ogen handler stub from OpenAPI operationId.

## Task

Implement ogen handler for operationId: `$ARGUMENTS`

## Steps

1. **Find operation** in OpenAPI spec (usually `api/openapi.yaml`).
 Note HTTP method, path, request body schema, response schemas.

2. **Find generated interface** in ogen output directory (usually `gen/`).
 method signature is: `func (h *Handler) <OperationId>(ctx context.Context, req *gen.<OpId>Req) (gen.<OpId>Res, error)`.

3. **Implement handler** in `internal/handler/<resource>.go`:
 - Accept generated request type, return generated response type.
 - Use `*slog.Logger` for logging (injected via fx).
 - Validate inputs beyond ogen's structural validation if needed.
 - Map domain errors to ogen error response types (RFC 9457 Problem Details).
 - Use `clock.Now(ctx)` — never `time.Now()`.

4. **Register** handler in fx module that provides `gen.Handler`.

5. **Write test** in `internal/handler/<resource>_test.go`:
 - Use `testutil/fxtest.New` to wire full handler.
 - Use `net/http/httptest` to call generated server.

6. **Lint**: `golangci-lint run ./internal/...` → 0 issues.

## Error mapping pattern

```go
var errNotFound = &gen.ErrorStatusCode{
    StatusCode: http.StatusNotFound,
    Response: gen.Error{
        Title:  "Not Found",
        Status: http.StatusNotFound,
    },
}
```
