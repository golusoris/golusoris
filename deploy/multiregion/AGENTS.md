<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — deploy/multiregion/

Reference **Pulumi (Go)** program for active/passive multi-region golusoris
deployment. Infra-as-code under isolated `deploy/go.mod`; `pulumi-aws` never
reaches framework root. Copy-and-adapt, like `deploy/terraform` / `deploy/pulumi`.

## What it deploys

- **Aurora Global Database** — writer cluster in `primaryRegion`, read-replica
 cluster in `secondaryRegion`; regional VPC placement + DSN secrets (`globaldb.go`).
- **Per-region app stack** — VPC + HTTPS :443 ALB/ECS + isolated private
  database subnets; regional ACM certificate; per-region `*aws.Provider`
  (`region.go`).
- **Global DNS failover** — aliases in caller-owned zone; ALB target-health
  evaluation; private HTTP `/readyz` target groups (`dns.go`).

## Usage

```bash
cd deploy/multiregion
pulumi config set primaryRegion us-east-1
pulumi config set secondaryRegion us-west-2
pulumi config set hostedZoneId Z0123456789EXAMPLE
pulumi config set primaryCertificateArn <primary-region-acm-arn>
pulumi config set secondaryCertificateArn <secondary-region-acm-arn>
pulumi up
```

## Notes

- Stack config in `Pulumi.yaml` / `Pulumi.prod.yaml`; outputs raw regional ALB
  DNS plus `globalURL=https://<domain>`.
- Output rename: `primaryURL` / `secondaryURL` -> raw `primaryALBDNS` /
  `secondaryALBDNS`; `globalDomain` -> `globalURL`.
- Failover is DNS-based (RPO/RTO per README); promote-replica runbook in
 `README.md`.
- `appImage` requires immutable `repository@sha256:<64 lowercase hex>` form.
- ECS receives region-local Aurora DSN through Secrets Manager `APP_DB_DSN`.
- Task definitions wait for secret/IAM resources; services wait for ALB listener
 association before ECS launch.
- framework ships **zero** new runtime deps; both Pulumi references share the
 separate `deploy` module and `deploy/internal/imageref` guard.
