# deploy/terraform

Reusable Terraform modules for the supporting infrastructure a golusoris app typically needs. Each module is cloud-agnostic where reasonable and has a clearly-scoped API.

## Modules

| Module | Purpose | Providers |
|---|---|---|
| [`modules/bucket`](./modules/bucket) | Versioned object-store bucket + lifecycle rules | AWS S3 (GCS / Azure Blob stubs included as comments) |
| [`modules/postgres`](./modules/postgres) | Managed Postgres (single-instance or HA) | AWS RDS (CloudSQL stub) |
| [`modules/redis`](./modules/redis) | Managed Redis or self-hosted fallback | AWS ElastiCache |
| [`modules/network`](./modules/network) | VPC + private subnets + NAT | AWS |

## Scope

These are **reference modules, not a framework**. Real infra varies enough that copy-and-adapt beats a giant swiss-army module. Use them as a starting point:

```hcl
module "app_bucket" {
  source = "github.com/golusoris/golusoris//deploy/terraform/modules/bucket?ref=v0.1.0"

  name            = "myapp-storage"
  versioning      = true
  expire_days     = 90
  tags            = { app = "myapp" }
}
```

## Why Terraform (vs. Pulumi / Crossplane)

The framework also ships a `deploy/pulumi/` example for Go-native IaC and `deploy/crossplane/` manifests for Kubernetes-native composition. Pick whichever model matches your team — they aren't mutually exclusive.

## Testing

Each module ships a `test/` directory with Terratest scaffolding (stubbed — fill in cloud credentials to run). See each module's README.
