<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — k8s/keda

KEDA external scaler (gRPC `externalscaler.ExternalScaler`) over jobs
queue depth. Scales River workers on outstanding jobs, incl. to/from zero.

## Conventions

- fx: `grpc.Module` (provides `*grpc.Server`) + `jobs.Module` or
 `jobs/sqlite.Module` + `jobs.MetricsModule` + `keda.Module`. Run it in
 always-on controller, not in scaled workers.
- Trigger metadata: `queue` (required), `targetDepth` (float > 0, default
 `k8s.keda.target_depth` 10), `activationDepth` (int >= 0, default 0).
- Depth = available + running (`jobs.DepthCollector.QueueDepth`), so
 running jobs keep >= 1 replica. Active when depth > activationDepth;
 inactive -> KEDA scales to zero (`minReplicaCount: 0`).
- Metric name `river-queue-<queue>`; `GetMetricSpec` returns target.
- Errors: bad metadata -> InvalidArgument; `jobs.ErrUnknownQueue` ->
 NotFound; depth read failure -> Unavailable (KEDA fallback applies).
- `StreamIsActive` sends on open + on change, re-reads every
 `k8s.keda.stream_interval` (10s). `StreamMetricSpec` -> Unimplemented
 (KEDA polls `GetMetricSpec`).
- Depth reads go through collector cache (`jobs.metrics.cache_ttl`), so
 scaler polls + Prometheus scrapes share one query.

## Codegen

- `internal/externalscalerpb/externalscaler.proto` = verbatim KEDA v2.21.0
 (commit `626ded5d783108bedf647e20bbc83f8e458e02e4`, Apache-2.0). Wire
 names are KEDA's contract: never rename package/service.
- Regenerate: `cd internal/externalscalerpb && buf generate`, then
 `gofumpt -w` + `gci write` (commands in `buf.gen.yaml`). protoc-gen-go
 pinned v1.36.3 (last release emitting no `unsafe`).

## Don't

- Don't import `github.com/kedacore/keda/v2` — operator dependency tree.
- Don't leave scaled queues out of `jobs.metrics.queues` — empty unknown
 queue -> NotFound -> no scale from zero.
