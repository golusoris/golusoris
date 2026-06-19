# deploy/pulumi

Reference [Pulumi](https://www.pulumi.com) (Go) program deploying a golusoris app on AWS. It provisions a VPC, an RDS PostgreSQL instance, an ElastiCache Redis replication group, and an ECS Fargate service behind an Application Load Balancer.

This is the Go-native complement to [`deploy/terraform`](../terraform) — **reference IaC, not a framework**. Copy and adapt.

## What it deploys

```
                    Internet
                       │
                  ┌────▼────┐  :80
                  │   ALB    │  (public subnets)
                  └────┬────┘
                       │ /readyz health check
              ┌────────▼────────┐
              │  ECS Fargate    │  (private subnets, rootless, read-only FS)
              │  golusoris app  │  ARM64, 0.25 vCPU / 512 MiB
              └───┬────────┬────┘
        APP_DB_DSN│        │APP_CACHE_ADDR
            ┌─────▼──┐  ┌──▼───────┐
            │  RDS   │  │ElastiCache│  (private subnets, encrypted, VPC-only SGs)
            │Postgres│  │  Redis    │
            └────────┘  └───────────┘
```

The DB DSN and Redis URL are written to AWS Secrets Manager and injected into the ECS task as the `APP_DB_DSN` and `APP_CACHE_ADDR` environment variables — the **same keys** the [Helm chart](../helm/values.yaml) uses, so a Pulumi-deployed binary reads its config identically to a Helm-deployed one.

## Prerequisites

- [Pulumi CLI](https://www.pulumi.com/docs/install/) ≥ 3.0
- Go 1.26
- AWS credentials with permission to create VPC / RDS / ElastiCache / ECS / IAM / Secrets Manager resources (env vars, `~/.aws/credentials`, or OIDC)

## `pulumi up` walkthrough

```bash
cd deploy/pulumi
pulumi stack init dev                                       # or: prod
pulumi config set --secret golusoris-app:dbPassword <strong-password>
pulumi config set golusoris-app:appImage ghcr.io/example/myapp:dev
pulumi up
```

On success, the stack exports:

| Output | Maps to app env var |
|---|---|
| `dsn` | `APP_DB_DSN` |
| `redisURL` | `APP_CACHE_ADDR` |
| `appURL` | — (the public ALB URL) |

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
| `appImage` | *(required)* | OCI image reference |
| `appReplicas` | `2` | Desired ECS task count |
| `appPort` | `8080` | Container port |
| `domain` | `""` | Informational; ALB DNS is exported regardless |
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
- **Redis** → Upstash (`pulumi/upstash`) or self-hosted. Keep the exported `redisURL` shape `redis://host:6379`.
- **App tier** → if you already run Kubernetes, deploy the app via the [Helm chart](../helm) and use Pulumi only for the data tier. See `AGENTS.md` and ADR for the ECS-vs-EKS rationale.

## Security notes

- The DB master password is a Pulumi **secret** (`--secret`), encrypted in stack state, surfaced to the task only via Secrets Manager → ECS secret refs. It is never a plaintext stack output.
- RDS and ElastiCache are encrypted at rest; their security groups allow ingress only from inside the VPC CIDR.
- The ECS task runs rootless (`user: 65534`) with a read-only root filesystem (§2.9).
