# Agent guide — grpc/

fx-wired gRPC server + client connection factory with OpenTelemetry tracing,
panic recovery, and structured slog logging built in. Opt-in via `grpc.Module`.

## Core types

| Type | Purpose |
|---|---|
| `Config` | Server config under `grpc.*` (env: `APP_GRPC_*`) — listen addr, TLS, message-size caps |
| `*grpc.Server` | The `google.golang.org/grpc` server, fx-provided; serves on fx Start, graceful-stops on fx Stop |
| `*ConnFactory` | Client-side dialer with OTel propagation; `Dial(ctx, target, ...)` returns a `*grpc.ClientConn` |
| `Module` | Provides `*grpc.Server` + `*ConnFactory`; requires `*config.Config` + `*slog.Logger` |

## Behaviour

- Interceptor chain (outermost first): OTel stats handler → slog logging → panic recovery, on both unary and stream.
- TLS is opt-in (`grpc.tls=true` + cert/key paths); when on, it pins `MinVersion = TLS 1.3`.
- Message size caps default to 4 MiB in and out; keepalive uses conservative internal-service defaults.
- `ConnFactory` dials with insecure transport credentials by default — override per call with `grpc.WithTransportCredentials(...)`.

## Usage

Register services after adding the module:

```go
fx.Invoke(func(s *grpc.Server) {
    mypb.RegisterMyServiceServer(s, &myImpl{})
})
```

## Codegen

Proto stubs are generated with [buf](https://buf.build), not committed by hand.
The shared, version-pinned config lives under `tools/`:

| File | Purpose |
|---|---|
| `tools/buf.gen.yaml` | Codegen plugins: `protocolbuffers/go` + `grpc/go`, `paths=source_relative`, output to `gen/go/`. Plugins are pinned to versions tracking the `protobuf` / `grpc` deps in go.mod. |
| `tools/buf.yaml` | Module + `buf lint` (STANDARD) + `buf breaking` (FILE) config. |

Copy both to the app repo root, then `buf lint && buf generate`. The
`/scaffold-grpc-service` skill walks the full proto → generate → implement →
fx-register flow.

## Don't

- Don't hand-write `*.pb.go` — regenerate via `buf generate` after editing protos.
- Don't bump the buf plugin pins in `tools/buf.gen.yaml` independently of the matching go.mod modules; bump them together.
- Don't call `time.Now()` in service implementations — use `clock.Now(ctx)`.
- Don't return raw Go errors from RPCs — map to `google.golang.org/grpc/status` codes.
