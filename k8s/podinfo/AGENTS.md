<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — k8s/podinfo

Reads k8s downward-API env vars and exposes them as `PodInfo` via fx.

## Conventions

- Required downward-API env block in deployment YAML: see package doc comment for canonical fragment. `deploy/helm/` ships it by default.
- `podinfo.IsInCluster()` is quick probe (checks for SA token file). Apps gate cluster-only behavior on it (e.g. only register leader election when in-cluster).
- Empty fields = downward API didn't wire that field. Don't panic — degrade.

## Don't

- Don't read `POD_NAME` etc. directly from app code if you can inject `PodInfo`. env-var convention is owned here.
- Don't add fields without corresponding downward-API mapping — struct is contract for what apps must wire in their deployment.
