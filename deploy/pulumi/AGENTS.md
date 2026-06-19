# Agent guide — deploy/pulumi/

Reference Pulumi (Go) program deploying a golusoris app on AWS: VPC + RDS
Postgres + ElastiCache Redis + ECS Fargate behind an ALB. **Infra-as-code, not
an fx module** — no `fx.Module`, no `_test.go` coverage gate, no clock/logger
injection. It is an **isolated Go module** (`deploy/pulumi/go.mod`) so the heavy
`pulumi-aws` dep tree never reaches the framework root.

## Layout

```
main.go        — pulumi.Run entrypoint; loadConfig → network → postgres → redis → app; exports dsn/redisURL/appURL
network.go     — VPC + 2 public + 2 private subnets + single NAT
postgres.go    — rds.Instance (encrypted, gp3, PerfInsights, IAM auth, 7-day backups); exports DSN
redis.go       — elasticache.ReplicationGroup (encrypted, automatic failover when multiAZ)
app.go         — ECS Fargate (rootless, read-only FS) + ALB + /readyz target group + Secrets Manager secret refs
Pulumi.yaml    — project manifest + config schema
Pulumi.dev.yaml / Pulumi.prod.yaml — example stack configs
```

## The two config surfaces

1. **Pulumi stack config** (`Pulumi.<stack>.yaml`, read via `config.New(ctx, "")`):
   `region`, `dbInstanceClass`, `dbStorageGB`, `dbEngineVersion`, `redisNodeType`,
   `appImage`, `appReplicas`, `appPort`, `domain`, `multiAZ`, `deletionProtection`,
   and the secret `dbPassword`.
2. **The deployed app's runtime env** (injected into the ECS task): `APP_DB_DSN`,
   `APP_CACHE_ADDR`, `APP_HTTP_ADDR` — identical to [`deploy/helm/values.yaml`](../helm/values.yaml)
   keys, so config never drifts between Helm and Pulumi deploys. **Keep these in
   lockstep** when either side changes (CLAUDE.md "keep docs in sync").

## Why Pulumi + pulumi-aws v7

- Pulumi gives Go-native IaC with the framework's fork-and-swap provider
  convention; `deploy/terraform/README.md` already advertises this example.
- `pulumi-aws` v7 is the broadest, most actively maintained Pulumi provider
  (Apache-2.0). Alternatives: AWS CDK (CloudFormation-bound, AWS-only, stack-size
  limits); `pulumi-aws-native` (auto-generated CFN shapes, clunkier for
  `rds.GlobalCluster`/`elasticache` — classic v7 has nicer ergonomics).
- **Isolation is the key choice**: the dep tree lives only in this module's
  `go.mod`. CI must confirm `go list -m` at the root never shows `pulumi-aws`.

## Conventions

- Small functions (Power-of-10 r4); every error wrapped `fmt.Errorf("pulumi: <what>: %w", err)`.
- `pulumi.Sprintf` builds the DSN/Redis URL outputs; `pulumi.All(...).ApplyT(...)`
  interpolates secret ARNs into the container-definitions + IAM-policy JSON.
- DB master password is a Pulumi secret → Secrets Manager → ECS secret ref. Never
  a plaintext stack output.

## Testing

- **Primary fast gate**: `pulumi preview` against the dev stack with a local
  backend (`PULUMI_BACKEND_URL=file://`) — catches config-schema + resource-graph
  errors without provisioning.
- `go build ./...`, `go vet ./...`, `gofumpt -l .`, `golangci-lint run` inside
  this dir — held to the same 0-lint bar as framework code.
- **Opt-in only** (label-gated, NOT default CI): a real `pulumi up`/`destroy`
  against a sandbox AWS account via the `auto` API. Costs real money; documented
  here, never run on every PR.
