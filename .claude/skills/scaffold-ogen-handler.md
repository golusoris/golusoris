Generate an ogen handler stub from an OpenAPI operationId.

## Task

Implement the ogen handler for operationId: `$ARGUMENTS`

## Steps

1. **Find the operation** in the OpenAPI spec (usually `api/openapi.yaml`).
   Note the HTTP method, path, request body schema, response schemas.

2. **Find the generated interface** in the ogen output directory (usually `gen/`).
   The method signature is: `func (h *Handler) <OperationId>(ctx context.Context, req *gen.<OpId>Req) (gen.<OpId>Res, error)`.

3. **Implement the handler** in `internal/handler/<resource>.go`:
   - Accept the generated request type, return the generated response type.
   - Use `*slog.Logger` for logging (injected via fx).
   - Validate inputs beyond ogen's structural validation if needed.
   - Map domain errors to ogen error response types (RFC 9457 Problem Details).
   - Use `clock.Now(ctx)` — never `time.Now()`.

4. **Register** the handler in the fx module that provides `gen.Handler`.

5. **Write a test** in `internal/handler/<resource>_test.go`:
   - Use `testutil/fxtest.New` to wire the full handler.
   - Use `net/http/httptest` to call the generated server.

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
