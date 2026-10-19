<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — k8s/cnpg/

CloudNativePG Cluster backup health as `statuspage.CheckFunc`. Reads
`postgresql.cnpg.io/v1` Cluster via `dynamic.Interface` (unstructured);
no CNPG module import. Field names verified against CNPG v1.30.1
`api/v1/cluster_types.go` + `cluster_conditions.go`.

## Key surface

| Symbol | Purpose |
| --- | --- |
| `BackupCheck(dyn, clk, namespace, cluster, maxAge)` | `(statuspage.CheckFunc, error)`; args validated up front |
| `ClusterGVR()` | `postgresql.cnpg.io/v1, Resource=clusters` (fake client registration) |
| `ErrClusterNotFound` `ErrNoBackup` `ErrBackupFailed` `ErrBackupStale` `ErrArchivingFailed` | `errors.Is` targets |

## Wiring

```go
dyn, err := dynamic.NewForConfig(restCfg) // *rest.Config from k8s/client
fn, err := cnpg.BackupCheck(dyn, clk, "db", "pg-main", 26*time.Hour)
reg.Register(statuspage.Check{Name: "cnpg-backup", Fn: fn}) // untagged
```

Untagged -> `/status` + prom check gauge, readiness unaffected.

## Evaluation order

1. `ContinuousArchiving` False -> `ErrArchivingFailed` (PITR broken).
2. `LastBackupSucceeded` False + reason `LastBackupFailed` ->
 `ErrBackupFailed` (message from CNPG).
3. Last success = `status.lastSuccessfulBackup` (in-tree methods only;
 deprecated, unset for plugins) else `LastBackupSucceeded` True
 `lastTransitionTime` (plugins set condition too).
4. Neither, reason `BackupStarted` -> start time vs `maxAge` (running
 backup passes until older than `maxAge`).
5. Nothing -> `ErrNoBackup`; age > `maxAge` -> `ErrBackupStale`.

Get bounded by 5s or caller deadline (registry default 2s).

## Don't

- No readiness tag: stale backup never pulls pods from service.
- No `maxAge` = schedule interval; allow backup runtime slack
 (daily schedule -> ~26h).
- RBAC: `get` on `clusters.postgresql.cnpg.io` in namespace.

## Tests

- `restore_kind_test.go` (`//go:build cnpgkind`): validates `BackupCheck` against live CNPG in kind cluster.
- `TestKind_BackupCheck_Positive`: backup health returns nil post-backup.
- `TestKind_BackupCheck_Negative`: missing cluster returns `ErrClusterNotFound`.
- `TestKind_BackupCheck_Boundary`: recovery cluster without backup returns `ErrNoBackup`.
- Skips when `KUBECONFIG` empty. CI step `Run CNPG backup restore in kind` (job `kubeconform`) runs `scripts/ci/cnpg-restore.sh`, fails on skip.
