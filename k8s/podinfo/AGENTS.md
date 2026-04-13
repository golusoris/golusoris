# Agent guide — k8s/podinfo

Reads the k8s downward-API env vars and exposes them as `PodInfo` via fx.

## Conventions

- Required downward-API env block in the deployment YAML — see the package doc comment for the canonical fragment. `deploy/helm/` (Step 21) ships it by default.
- `podinfo.IsInCluster()` is a quick probe (checks for the SA token file). Apps gate cluster-only behavior on it (e.g. only register leader election when in-cluster).
- Empty fields = downward API didn't wire that field. Don't panic — degrade.

## Don't

- Don't read `POD_NAME` etc. directly from app code if you can inject `PodInfo`. The env-var convention is owned here.
- Don't add fields without a corresponding downward-API mapping — the struct is a contract for what apps must wire in their deployment.
