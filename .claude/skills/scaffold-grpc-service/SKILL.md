---
name: scaffold-grpc-service
description: Use when adding new gRPC service to golusoris app — walks proto definition → buf generate → implement → fx-register.
---

# Scaffold a gRPC service

Add gRPC service end-to-end: define proto, generate stubs with buf,
implement server, and register it into fx graph.

## Task

Scaffold gRPC service named: `$ARGUMENTS`

## Prerequisites

- `golusoris/grpc` is wired (app imports `grpc.Module` — provides
 `*grpc.Server` + `*grpc.ConnFactory`). See `grpc/AGENTS.md`.
- `buf` is installed (`brew install bufbuild/buf/buf` or see https://buf.build/docs/installation).
- repo has copied `tools/buf.gen.yaml` + `tools/buf.yaml` to its root (see
 `grpc/AGENTS.md` § Codegen for pinned-plugin config).

## Steps

1. **Define proto** in `proto/<service>/v1/<service>.proto`:
 - `syntax = "proto3";`
 - `package <service>.v1;` — versioned suffix is enforced by `buf lint`.
 - `option go_package = "<module>/gen/go/<service>/v1;<service>v1";`
 - Define `service <Name>Service { rpc ... }` plus request/response messages.
 - Enum zero values end in `_UNSPECIFIED` (buf lint `enum_zero_value_suffix`).

2. **Lint proto**: `buf lint` → 0 issues. Fix naming before generating.

3. **Generate stubs**: `buf generate` (uses `tools/buf.gen.yaml`).
 Output lands in `gen/go/<service>/v1/` as `*.pb.go` + `*_grpc.pb.go`
 (`paths=source_relative`). Commit generated code.

4. **Implement server** in `internal/grpcsvc/<service>.go`:
 - Define `type <name>Server struct { ... }` implementing generated
 `<Name>ServiceServer` interface. fragment sets
 `require_unimplemented_servers=false`, so implement every RPC explicitly.
 - Inject `*slog.Logger` (and any deps) via fx constructor.
 - Use `clock.Now(ctx)` — never `time.Now()`.
 - Map domain errors to gRPC status codes via `status.Error(codes.X, ...)`;
 wrap underlying errors with `%w` in message.

5. **Register** service into fx with `fx.Invoke` that calls  generated `Register<Name>ServiceServer(s, impl)` against `*grpc.Server`:

   ```go
   fx.Invoke(func(s *grpc.Server, impl *fooServer) {
       foov1.RegisterFooServiceServer(s, impl)
   }),
   ```

 Provide `*fooServer` with `fx.Provide(newFooServer)` in same module.

6. **Write test** in `internal/grpcsvc/<service>_test.go`:
 - Use in-process `bufconn` listener (`google.golang.org/grpc/test/bufconn`)
 so test is hermetic — no real TCP port.
 - Dial via `grpc.ConnFactory` or direct `bufconn` dialer, call RPC,
 assert response + status codes.

7. **Lint**: `golangci-lint run ./internal/...` → 0 issues, then `make test`.

## Status-code mapping pattern

```go
import (
    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/status"
)

func (s *fooServer) GetFoo(ctx context.Context, req *foov1.GetFooRequest) (*foov1.GetFooResponse, error) {
    foo, err := s.repo.Get(ctx, req.GetId())
    if errors.Is(err, repo.ErrNotFound) {
        return nil, status.Errorf(codes.NotFound, "foo %q not found", req.GetId())
    }
    if err != nil {
        return nil, status.Errorf(codes.Internal, "get foo: %v", err)
    }
    return &foov1.GetFooResponse{Foo: toProto(foo)}, nil
}
```
