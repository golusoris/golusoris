<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# deploy/multiregion

Reference [Pulumi](https://www.pulumi.com) (Go) program for an **active/passive**
two-region golusoris deployment: an Aurora Global Database (writer in the primary
region, read replica in the secondary), a per-region app + TLS-only ALB stack
reused across both regions, and Route53 DNS failover in a caller-owned zone.

> **Cost caveat.** The Aurora Global Database is expensive and slow to provision
> (~20-40 minutes, cross-region replication charges). The opt-in real-`up` test
> is label-gated to avoid burning money and CI minutes. Treat this as a
> copy-and-adapt reference, not a one-click deploy.

Go dependencies and the immutable-image guard are rooted at `deploy/go.mod`
and `deploy/internal/imageref`. Commands still run from this directory. When
copying the reference, retain the parent `go.mod`, `go.sum`, and `internal/imageref`
beside the copied program directory.

## Topology

```text
                         ┌─────────────────────────┐
                         │ caller Route53 zone      │
                         │  app.example.com (A)     │
                         │  failover routing        │
                         └───────┬──────────┬───────┘
                  PRIMARY record │          │ SECONDARY record
                 (ALB target health)       (ALB target health)
                                 │          │
                   ┌─────────────▼──┐    ┌──▼─────────────┐
                   │  us-east-1      │    │  us-west-2      │
                   │ HTTPS :443 ALB  │    │ HTTPS :443 ALB  │  (passive)
                   │ + ECS           │    │ + ECS           │
                   └────────┬────────┘    └────────┬───────┘
                            │                      │
                   ┌────────▼────────┐    ┌────────▼────────┐
                   │ private Aurora  │═══▶│ private Aurora  │  (read-only)
                   │ writer          │    │ replica         │
                   │ (primary)       │    │ (secondary)     │
                   └────────┬────────┘    └─────────────────┘
                            └──── Aurora Global Database ────┘
```

## Failover model

- Route53 failover aliases set `EvaluateTargetHealth=true`; no public direct
  health-check endpoint is created.
- Each ALB evaluates its private HTTP `/readyz` target group. The `PRIMARY`
  alias serves traffic while its ALB targets are healthy; Route53 returns the
  `SECONDARY` alias when the primary target becomes unhealthy.
- **RPO**: near-zero for committed writes within the Aurora replication lag
  (typically < 1 s cross-region). **RTO**: DNS TTL + health-check detection
  (≈ 1-3 min) for read traffic; **writes require a manual replica promotion** —
  see the runbook below.

## Promote-replica runbook (planned or DR)

1. Confirm the primary region is truly down (or you are doing a planned failover).
2. Detach + promote the secondary cluster to a standalone writer:

   ```bash
   aws rds remove-from-global-cluster \
     --global-cluster-identifier golusoris-global \
     --db-cluster-identifier <secondary-cluster-arn> \
     --region us-west-2
   ```

3. Wait for promotion to complete, then verify the secondary app. Its
   `APP_DB_DSN` already targets that region's cluster endpoint, so the endpoint
   and secret remain valid when the replica becomes the writer.
4. Once the primary region recovers, rebuild it as the new replica (re-add to the
   global cluster) and, when ready, fail back.

## Stack config

| Key | Default | Purpose |
| --- | --- | --- |
| `primaryRegion` | `us-east-1` | Aurora writer + active app stack |
| `secondaryRegion` | `us-west-2` | Aurora read replica + passive app stack |
| `domain` | *(required)* | Failover record set, e.g. `app.example.com` |
| `hostedZoneId` | *(required)* | Caller-owned Route53 hosted-zone ID |
| `primaryCertificateArn` | *(required)* | ACM certificate in `primaryRegion` covering `domain` |
| `secondaryCertificateArn` | *(required)* | ACM certificate in `secondaryRegion` covering `domain` |
| `dbInstanceClass` | `db.r6g.large` | Aurora cluster instance class |
| `dbEngineVersion` | `16.6` | Aurora PostgreSQL version |
| `appImage` | *(required)* | Immutable `repository@sha256:<64 lowercase hex>` image deployed in both regions |
| `appPort` | `8080` | Container port |
| `dbPassword` | *(required secret)* | Aurora master password — `pulumi config set --secret` |

```bash
cd deploy/multiregion
pulumi stack init prod
pulumi config set --secret golusoris-multiregion:dbPassword <strong-password>
pulumi config set golusoris-multiregion:domain app.example.com
pulumi config set golusoris-multiregion:hostedZoneId Z0123456789EXAMPLE
pulumi config set golusoris-multiregion:primaryCertificateArn arn:aws:acm:us-east-1:123456789012:certificate/00000000-0000-0000-0000-000000000000
pulumi config set golusoris-multiregion:secondaryCertificateArn arn:aws:acm:us-west-2:123456789012:certificate/11111111-1111-1111-1111-111111111111
pulumi config set golusoris-multiregion:appImage ghcr.io/example/myapp@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
pulumi up
```

Outputs: `primaryALBDNS`, `secondaryALBDNS`, `globalURL`. The ALB outputs are
raw diagnostics; clients use `globalURL` (`https://<domain>`).

Each Aurora cluster shares its regional VPC placement with ECS. The cluster is
not public. A region-local Secrets Manager value supplies `APP_DB_DSN`, and the
ECS execution role can read only that secret.

## Provider swap

The multi-region pattern uses one explicit `aws.NewProvider` per region (the
canonical Pulumi approach). To target another cloud, swap the per-region provider
and replace `globaldb.go`'s Aurora Global Database with that cloud's cross-region
replication primitive (e.g. CloudSQL cross-region replicas). The
[`dns.go`](./dns.go) failover record carries a commented latency-routing variant.
