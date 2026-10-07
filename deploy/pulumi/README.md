<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# deploy/pulumi

Reference [Pulumi](https://www.pulumi.com) (Go) program deploying a golusoris app on AWS. It provisions a VPC, an RDS PostgreSQL instance, an ElastiCache Redis replication group, and an ECS Fargate service behind a TLS-only Application Load Balancer. A Route53 alias is added to a caller-owned hosted zone.

This is the Go-native complement to [`deploy/terraform`](../terraform) — **reference IaC, not a framework**. Copy and adapt.

Go dependencies and the immutable-image guard are rooted at `deploy/go.mod`
and `deploy/internal/imageref`. Commands still run from this directory. When
copying the reference, retain the parent `go.mod`, `go.sum`, and `internal/imageref`
beside the copied program directory.

## What it deploys

```text
                    Internet
                       │
             caller-owned Route53 zone
                       │ alias (target health)
                  ┌────▼────┐  HTTPS :443
                  │   ALB    │  ACM certificate, TLS 1.2/1.3
                  └────┬────┘
                       │ /readyz health check
              ┌────────▼────────┐
              │  ECS Fargate    │  (private subnets, rootless, read-only FS)
              │  golusoris app  │  ARM64, 0.25 vCPU / 512 MiB
              └───┬────────┬────┘
        APP_DB_DSN│        │APP_CACHE_REDIS_ADDR
            ┌─────▼──┐  ┌──▼───────┐
            │  RDS   │  │ElastiCache│  (private subnets, encrypted, VPC-only SGs)
            │Postgres│  │  Redis    │
            └────────┘  └───────────┘
```

The DB DSN and bare Redis address are written to AWS Secrets Manager and
injected into the ECS task as `APP_DB_DSN` and `APP_CACHE_REDIS_ADDR`.
`APP_CACHE_REDIS_TLS=true` enables verified TLS for the TLS-only ElastiCache
endpoint. These names map to `db.dsn`, `cache.redis.addr`, and
`cache.redis.tls`.

## Prerequisites

- [Pulumi CLI](https://www.pulumi.com/docs/install/) ≥ 3.0
- Go 1.27
- A Route53 hosted zone and a regional ACM certificate covering the configured domain
- AWS credentials with permission to create VPC / RDS / ElastiCache / ECS / IAM / Secrets Manager / Route53 resources (env vars, `~/.aws/credentials`, or OIDC)

## `pulumi up` walkthrough

```bash
cd deploy/pulumi
pulumi stack init dev                                       # or: prod
pulumi config set --secret golusoris-app:dbPassword <strong-password>
pulumi config set golusoris-app:appImage ghcr.io/example/myapp@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
pulumi config set golusoris-app:domain app.example.com
pulumi config set golusoris-app:hostedZoneId Z0123456789EXAMPLE
pulumi config set golusoris-app:certificateArn arn:aws:acm:us-east-1:123456789012:certificate/00000000-0000-0000-0000-000000000000
pulumi up
```

On success, the stack exports:

| Output | Maps to app env var |
|---|---|
| `dsn` | `APP_DB_DSN` |
| `redisAddress` | `APP_CACHE_REDIS_ADDR` |
| `redisURL` | Operator-facing `rediss://` URL |
| `appURL` | — (`https://<domain>`) |

Tear down with `pulumi destroy`.

## Stack config

Set per stack via `pulumi config set golusoris-app:<key> <value>`. See [`Pulumi.dev.yaml`](./Pulumi.dev.yaml) / [`Pulumi.prod.yaml`](./Pulumi.prod.yaml) for examples.

| Key | Default | Purpose |
|---|---|---|
| `region` | `us-east-1` | AWS region |
| `dbInstanceClass` | `db.t4g.small` | RDS instance class |
| `dbStorageGB` | `20` | RDS allocated storage (GiB) |
| `dbEngineVersion` | `17.2` | PostgreSQL version |
| `redisNodeType` | `cache.t3.micro` | ElastiCache node type |
| `appImage` | *(required)* | Immutable `repository@sha256:<64 lowercase hex>` image reference |
| `appReplicas` | `2` | Desired ECS task count |
| `appPort` | `8080` | Container port |
| `domain` | *(required)* | Public application domain |
| `hostedZoneId` | *(required)* | Caller-owned Route53 hosted-zone ID |
| `certificateArn` | *(required)* | Regional ACM certificate ARN covering `domain` |
| `multiAZ` | `false` | RDS Multi-AZ + ElastiCache automatic failover |
| `deletionProtection` | `false` | Guard the DB against deletion |
| `dbPassword` | *(required secret)* | DB master password — `pulumi config set --secret` |

### YAML-runtime twin

Teams that prefer the YAML runtime over Go can express the same resources without the helper functions. The Go program is canonical because [`deploy/multiregion`](../multiregion) reuses one per-region stack function across providers — awkward in YAML. A minimal YAML twin looks like:

```yaml
name: golusoris-app
runtime: yaml
resources:
  vpc:
    type: aws:ec2:Vpc
    properties:
      cidrBlock: 10.0.0.0/16
      enableDnsHostnames: true
  # ... subnets, rds:Instance, elasticache:ReplicationGroup, ecs:Service ...
```

## Provider swap

The data tier is the easiest thing to point elsewhere:

- **Postgres** → CloudSQL (`gcp:sql:DatabaseInstance`) or Neon (`pulumiverse/neon`). Keep the exported `dsn` shape `postgres://…?sslmode=require`.
- **Redis** → Upstash (`pulumi/upstash`) or self-hosted. Keep
  `redisAddress` as bare `host:port` and `redisURL` as `rediss://host:port`.
- **App tier** → if you already run Kubernetes, deploy the app via the [Helm chart](../helm) and use Pulumi only for the data tier. See `AGENTS.md` and ADR for the ECS-vs-EKS rationale.

## Security notes

- The DB master password is a Pulumi **secret** (`--secret`), encrypted in stack state, surfaced to the task only via Secrets Manager → ECS secret refs. It is never a plaintext stack output.
- RDS and ElastiCache are encrypted at rest; ElastiCache also requires verified
  TLS in transit. Their security groups allow ingress only from inside the VPC CIDR.
- The public ALB accepts only HTTPS on port 443 with the pinned modern policy;
  ALB-to-task traffic and `/readyz` target health checks remain private HTTP.
- The ECS task runs rootless (`user: 65534`) with a read-only root filesystem (§2.9).
