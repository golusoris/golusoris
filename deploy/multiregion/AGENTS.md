# Agent guide — deploy/multiregion/

Reference **Pulumi (Go)** program for an active/passive multi-region golusoris
deployment. Infra-as-code, **own `go.mod`** (pulumi-aws lives here, never in the
framework root). Copy-and-adapt, like `deploy/terraform` / `deploy/pulumi`.

## What it deploys

- **Aurora Global Database** — writer cluster in `primaryRegion`, read-replica
  cluster in `secondaryRegion` (`globaldb.go`).
- **Per-region app stack** — VPC + ECS/ALB + app service, reused for both regions
  via a per-region `*aws.Provider` (`region.go`).
- **Global DNS failover** — Route53 primary/secondary failover records gated on a
  per-region `/readyz` health check (`dns.go`).

## Usage

```bash
cd deploy/multiregion
pulumi config set primaryRegion us-east-1
pulumi config set secondaryRegion us-west-2
pulumi up
```

## Notes

- Stack config in `Pulumi.yaml` / `Pulumi.prod.yaml`; outputs `primaryURL` /
  `secondaryURL` / `globalDomain`.
- Failover is DNS-based (RPO/RTO per the README); promote-replica runbook in
  `README.md`.
- The framework ships **zero** new runtime deps from this — it's a separate module.
