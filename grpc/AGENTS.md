<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — grpc/

fx-wired gRPC server + client connection factory with OpenTelemetry tracing,
panic recovery, and structured slog logging built in. Opt-in via `grpc.Module`.

## Core types

| Type | Purpose |
| --- | --- |
| `Config` | Server config under `grpc.*` (env: `APP_GRPC_*`) — listen addr, TLS/mTLS, message-size caps, keepalive, health |
| `KeepaliveConfig` | `grpc.keepalive.*` — connection age + grace, ping time/timeout, client ping policy |
| `Infinite` | negative-duration sentinel: never rotate / unlimited grace / no server pings |
| `CompoundKeys()` | snake_case `grpc.*` keys for `config.Options.CompoundKeys`; env overrides need it |
| `*grpc.Server` | The `google.golang.org/grpc` server, fx-provided; serves on fx Start, graceful-stops on fx Stop |
| `*ConnFactory` | Client-side dialer with OTel propagation; `Dial(ctx, target, ...)` returns a `*grpc.ClientConn` |
| `ClientConfig` | `grpc.client.*` — TLS/mTLS files, `server_name`, keepalive, retry; `NewConnFactoryWithConfig(cfg, logger)` |
| `Module` | Provides `*grpc.Server` + `*ConnFactory`; requires `*config.Config` + `*slog.Logger` |

## Behaviour

- Interceptor chain (outermost first): OTel stats handler → slog logging → panic recovery, on both unary and stream.
- TLS is opt-in (`grpc.tls=true` + `cert_file`/`key_file`); pins TLS 1.3. Files reload on handshake via `core/tlsx` (30s min interval); bad rotation keeps last good cert.
- mTLS: `grpc.ca_file` + `grpc.client_auth` (`none`, `request`, `require_any`, `verify_if_given`, `require_and_verify`). CA without mode = `require_and_verify`. Verifying mode without CA fails startup.
- Message size caps default to 4 MiB in and out.
- Keepalive defaults = pre-#589 values: age 2m, grace 5s, time 1m, timeout 20s; enforcement min_time 5m, no pings without stream. Zero = default. Negative age/grace/time = never (`Infinite`). Negative timeout/min_time fails startup.
- Long RPCs (minutes): set `keepalive.max_connection_age` negative, or keep rotation and set `max_connection_age_grace` negative. App `ProvideServerOption(grpc.KeepaliveParams(...))` still overrides (appended last).
- `k8s/health.Module` wired -> stop hook wrapped by its `core/drain.Gate`: `GracefulStop` starts after `health.drain.delay`; stop ctx expiry -> hard `Stop`.
- `grpc.health=true` registers `grpc.health.v1`. Optional `*statuspage.Registry` in graph -> Check("") runs readiness-tagged checks (`health.Serving`); gate wired -> its `shutdown` check fails Check("") for whole drain window. fx Stop flips NOT_SERVING before `GracefulStop`. Off by default: app-registered health service would collide.
- `NewConnFactory()` dials insecure. Module factory reads `grpc.client.*`; zero config = same insecure factory. Per-call `Dial(..., grpc.WithTransportCredentials(...))` still overrides.
- Client TLS: `grpc.client.tls=true`; no files = system roots. Files reload per new connection (custom creds rebuild TLS config per handshake), so rotated client cert + CA bundle reach next dial. Files without `tls=true` fail construction.
- `grpc.client.server_name` -> `grpc.WithAuthority`: TLS name check + `:authority`.
- Client keepalive off unless `grpc.client.keepalive.time > 0`. Keep time >= server `keepalive.min_time` (5m) or server sends GOAWAY `too_many_pings`.
- `grpc.client.retry.max_attempts > 1` installs default service config: all methods, retry `UNAVAILABLE`, backoff 100ms -> 1s x2. grpc-go caps attempts at 5. Resolver-supplied service config wins.

## Usage

Register services after adding module:

```go
fx.Invoke(func(s *grpc.Server) {
    mypb.RegisterMyServiceServer(s, &myImpl{})
})
```

## Codegen

Proto stubs are generated with [buf](https://buf.build), not committed by hand.
shared, version-pinned config lives under `tools/`:

| File | Purpose |
| --- | --- |
| `tools/buf.gen.yaml` | Codegen plugins: `protocolbuffers/go` + `grpc/go`, `paths=source_relative`, output to `gen/go/`. Plugins are pinned to versions tracking the `protobuf` / `grpc` deps in go.mod. |
| `tools/buf.yaml` | Module + `buf lint` (STANDARD) + `buf breaking` (FILE) config. |

Copy both to app repo root, then `buf lint && buf generate`. `/scaffold-grpc-service` skill walks full proto → generate → implement →
fx-register flow.

## Don't

- Don't hand-write `*.pb.go` — regenerate via `buf generate` after editing protos.
- Don't bump buf plugin pins in `tools/buf.gen.yaml` independently of matching go.mod modules; bump them together.
- Don't call `time.Now()` in service implementations — use `clock.Now(ctx)`.
- Don't return raw Go errors from RPCs — map to `google.golang.org/grpc/status` codes.
