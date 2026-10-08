<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# container/registry/credentials/acr

Azure Container Registry provider for `container/registry/credentials` chain.

```go
p := acr.New(tokenCredential, acr.Options{TenantID: "..."}, nil) // any azcore.TokenCredential
fx.New(..., credentials.Module, acr.Module)                     // azidentity.NewDefaultAzureCredential
```

Flow: Entra token (scope `https://containerregistry.azure.net/.default`) ->
`POST https://<registry>/oauth2/exchange` form `grant_type=access_token`,
`service=<registry>`, `access_token`, optional `tenant` -> JSON `refresh_token`.
Credential: user `00000000-0000-0000-0000-000000000000`, password = refresh token,
`ExpiresAt` = Entra token expiry (conservative).

- Hosts: `*.azurecr.io`, `*.azurecr.cn`, `*.azurecr.us`; others -> `credentials.ErrNoCredential`.
- Exchange bounded by `Options.Timeout` (default 30s); body capped 1 MiB; non-200 -> `ErrExchange`.
- AKS workload identity: webhook injects `AZURE_CLIENT_ID`, `AZURE_TENANT_ID`, `AZURE_FEDERATED_TOKEN_FILE`.
- Config `container.registry.credentials.acr.*`: `tenant_id`, `timeout`.
- Tests fake Entra (`azcore.TokenCredential`) and exchange endpoint; azidentity WI flow itself untested here (needs HTTPS authority).
