# Agent guide — k8s/client

Resolves a `*rest.Config` + `kubernetes.Interface` from in-cluster, KUBECONFIG, or `~/.kube/config` (in that order).

## Workload identity

The Go SDK side is intentionally minimal — cloud workload identity works
through the standard in-cluster path on each platform. The framework
doesn't reach into cloud SDKs:

| Platform | Mechanism | Notes |
|---|---|---|
| GKE Workload Identity | Metadata server exchanges the SA token for a Google identity | Pod sees a normal SA token mount; cloud SDKs use the metadata endpoint |
| EKS IRSA | Projected SA token (`AWS_WEB_IDENTITY_TOKEN_FILE`) + `AWS_ROLE_ARN` | aws-sdk-go-v2 (`storage/`, `secrets/`) reads these env vars + the projected token |
| Azure AD WI | Projected SA token (`AZURE_FEDERATED_TOKEN_FILE`) | Azure SDK consumes them |
| In-cluster (no cloud) | SA token at `/var/run/secrets/...` | Standard k8s API access |

So the client package handles **k8s-API access**; cloud-API access is each
SDK's job, with the token mount already in place.

## Conventions

- `Source` field on `Resolved` reports `in-cluster` vs `kubeconfig` —
  log this on startup so deploys can verify they're on the right path.
- Default QPS=20, Burst=30 match client-go's controller defaults.
- Apps that build many informers should bump these (typical: 100/200).

## Don't

- Don't depend on a specific cloud's SDK in this package. The token
  mount is the abstraction.
