# deploy/multiregion

Reference [Pulumi](https://www.pulumi.com) (Go) program for an **active/passive**
two-region golusoris deployment: an Aurora Global Database (writer in the primary
region, read replica in the secondary), a per-region app + ALB stack reused
across both regions, and Route53 DNS failover.

> **Cost caveat.** The Aurora Global Database is expensive and slow to provision
> (~20-40 minutes, cross-region replication charges). The opt-in real-`up` test
> is label-gated to avoid burning money and CI minutes. Treat this as a
> copy-and-adapt reference, not a one-click deploy.

## Topology

```
                         ┌─────────────────────────┐
                         │  Route53 hosted zone     │
                         │  app.example.com (A)     │
                         │  failover routing        │
                         └───────┬──────────┬───────┘
                  PRIMARY record │          │ SECONDARY record
              (health check /readyz)    (health check /readyz)
                                 │          │
                   ┌─────────────▼──┐    ┌──▼─────────────┐
                   │  us-east-1      │    │  us-west-2      │
                   │  ALB → ECS app  │    │  ALB → ECS app  │  (passive)
                   └────────┬────────┘    └────────┬───────┘
                            │                      │
                   ┌────────▼────────┐    ┌────────▼────────┐
                   │ Aurora writer   │═══▶│ Aurora replica  │  (read-only)
                   │ (primary)       │    │ (secondary)     │
                   └────────┬────────┘    └─────────────────┘
                            └──── Aurora Global Database ────┘
```

## Failover model

- Route53 health checks ping each region's ALB on HTTP `/readyz` every 30 s
  (3 failures → unhealthy).
- The `PRIMARY` failover record serves traffic while its health check is healthy.
  On failure, Route53 returns the `SECONDARY` record automatically.
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
3. Point the app's `APP_DB_DSN` at the promoted cluster's writer endpoint
   (update the secondary stack's secret + redeploy its ECS service).
4. Once the primary region recovers, rebuild it as the new replica (re-add to the
   global cluster) and, when ready, fail back.

## Stack config

| Key | Default | Purpose |
|---|---|---|
| `primaryRegion` | `us-east-1` | Aurora writer + active app stack |
| `secondaryRegion` | `us-west-2` | Aurora read replica + passive app stack |
| `domain` | *(required)* | Failover record set, e.g. `app.example.com` |
| `dbInstanceClass` | `db.r6g.large` | Aurora cluster instance class |
| `dbEngineVersion` | `16.6` | Aurora PostgreSQL version |
| `appImage` | *(required)* | OCI image deployed in both regions |
| `appPort` | `8080` | Container port |
| `dbPassword` | *(required secret)* | Aurora master password — `pulumi config set --secret` |

```bash
cd deploy/multiregion
pulumi stack init prod
pulumi config set --secret golusoris-multiregion:dbPassword <strong-password>
pulumi config set golusoris-multiregion:domain app.example.com
pulumi config set golusoris-multiregion:appImage ghcr.io/example/myapp:v1.0.0
pulumi up
```

Outputs: `primaryURL`, `secondaryURL`, `globalDomain`.

## Provider swap

The multi-region pattern uses one explicit `aws.NewProvider` per region (the
canonical Pulumi approach). To target another cloud, swap the per-region provider
and replace `globaldb.go`'s Aurora Global Database with that cloud's cross-region
replication primitive (e.g. CloudSQL cross-region replicas). The
[`dns.go`](./dns.go) failover record carries a commented latency-routing variant.
