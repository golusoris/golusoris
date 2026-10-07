<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# Agent guide — k8s/client

Resolves `*rest.Config` + `kubernetes.Interface` from in-cluster, KUBECONFIG, or `~/.kube/config` (in that order).

## Workload identity

Go SDK side is intentionally minimal — cloud workload identity works
through standard in-cluster path on each platform. framework
doesn't reach into cloud SDKs:

| Platform | Mechanism | Notes |
| --- | --- | --- |
| GKE Workload Identity | Metadata server exchanges SA token for Google identity | Pod sees normal SA token mount; cloud SDKs use metadata endpoint |
| EKS IRSA | Projected SA token (`AWS_WEB_IDENTITY_TOKEN_FILE`) + `AWS_ROLE_ARN` | aws-sdk-go-v2 (`storage/`, `secrets/`) reads these env vars + projected token |
| Azure AD WI | Projected SA token (`AZURE_FEDERATED_TOKEN_FILE`) | Azure SDK consumes them |
| In-cluster (no cloud) | SA token at `/var/run/secrets/...` | Standard k8s API access |

So client package handles **k8s-API access**; cloud-API access is each
SDK's job, with token mount already in place.

## Conventions

- `Source` field on `Resolved` reports `in-cluster` vs `kubeconfig` —
 log this on startup so deploys can verify they're on right path.
- Default QPS=20, Burst=30 match client-go's controller defaults.
- Apps that build many informers should bump these (typical: 100/200).

## Don't

- Don't depend on specific cloud's SDK in this package. token
 mount is abstraction.
