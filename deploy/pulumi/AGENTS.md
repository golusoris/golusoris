<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — deploy/pulumi/

Reference Pulumi (Go) program deploying golusoris app on AWS: VPC + RDS
Postgres + ElastiCache Redis + ECS Fargate behind ALB. **Infra-as-code, not
fx module** — no `fx.Module` or clock/logger injection. Both Pulumi references
share isolated `deploy/go.mod`; heavy `pulumi-aws` dependencies never reach
framework root.

## Layout

```
main.go        — pulumi.Run entrypoint; loadConfig → network → postgres → redis → app; exports dsn/redisURL/appURL
network.go     — VPC + 2 public + 2 private subnets + single NAT
postgres.go    — rds.Instance (encrypted, gp3, PerfInsights, IAM auth, 7-day backups); exports DSN
redis.go       — TLS-only elasticache.ReplicationGroup; bare app address + rediss operator URL
app.go         — ECS Fargate (rootless, read-only FS) + ALB + /readyz target group + Secrets Manager secret refs
app.go edge    — HTTPS :443 + caller-zone alias; HTTP app port + private /readyz
Pulumi.yaml    — project manifest + config schema
Pulumi.dev.yaml / Pulumi.prod.yaml — example stack configs
../internal/imageref — shared immutable application-image validator
../go.mod / ../go.sum — isolated dependency authority for both Pulumi references
```

## The two config surfaces

1. **Pulumi stack config** (`Pulumi.<stack>.yaml`, read via `config.New(ctx, "")`):
 `region`, `dbInstanceClass`, `dbStorageGB`, `dbEngineVersion`, `redisNodeType`,
 `appImage`, `appReplicas`, `appPort`, `domain`, `hostedZoneId`, `certificateArn`,
 `multiAZ`, `deletionProtection`, and secret `dbPassword`.
2. **deployed app's runtime env** (injected into ECS task): `APP_DB_DSN`,
 `APP_CACHE_REDIS_ADDR`, `APP_CACHE_REDIS_TLS`, `APP_HTTP_ADDR`. They map to
 `db.dsn`, `cache.redis.addr`, `cache.redis.tls`, and `http.addr`. Keep runtime
 config names synchronized.

## Why Pulumi + pulumi-aws v7

- Pulumi gives Go-native IaC with framework's fork-and-swap provider
 convention; `deploy/terraform/README.md` already advertises this example.
- `pulumi-aws` v7 is broadest, most actively maintained Pulumi provider
 (Apache-2.0). Alternatives: AWS CDK (CloudFormation-bound, AWS-only, stack-size
 limits); `pulumi-aws-native` (auto-generated CFN shapes, clunkier for
 `rds.GlobalCluster`/`elasticache` — classic v7 has nicer ergonomics).
- **Isolation is key choice**: dep tree lives only in `deploy/go.mod`. CI must
 confirm `go list -m` at root never shows `pulumi-aws`.

## Conventions

- Small functions (Power-of-10 r4); every error wrapped `fmt.Errorf("pulumi: <what>: %w", err)`.
- `pulumi.Sprintf` builds DSN/Redis URL outputs; `pulumi.All(...).ApplyT(...)`
 interpolates secret ARNs into container-definitions + IAM-policy JSON.
- DB master password is Pulumi secret → Secrets Manager → ECS secret ref. Never
 plaintext stack output.
- TLS-only Redis injects bare `host:port` plus `APP_CACHE_REDIS_TLS=true`;
 `redisURL` remains an operator-facing `rediss://` output.
- Task definitions depend on secret versions and both execution-role policies;
  ECS services depend on the ALB listener association.
- `appImage` requires immutable `repository@sha256:<64 lowercase hex>` form.
- Public edge = caller-owned Route53 alias -> HTTPS :443 ALB -> HTTP app port.
- ACM certificate must cover `domain` and exist in `region`; alias uses
  `hostedZoneId` with target-health evaluation.

## Testing

- **Primary fast gate**: `pulumi preview` against dev stack with local
 backend (`PULUMI_BACKEND_URL=file://`) — catches config-schema + resource-graph
 errors without provisioning.
- `go build ./...`, `go vet ./...`, `gofumpt -l .`, `golangci-lint run` inside
 this dir — held to same 0-lint bar as framework code.
- **Opt-in only** (label-gated, NOT default CI): real `pulumi up`/`destroy`
 against sandbox AWS account via `auto` API. Costs real money; documented
 here, never run on every PR.
