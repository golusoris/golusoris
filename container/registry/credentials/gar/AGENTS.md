<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# container/registry/credentials/gar

Google Artifact Registry / Container Registry provider for `container/registry/credentials` chain.

```go
p, err := gar.NewDefault()          // Application Default Credentials, cloud-platform scope
p := gar.New(tokenSource)           // any oauth2.TokenSource
fx.New(..., credentials.Module, gar.Module)
```

- Hosts: `*-docker.pkg.dev`, `gcr.io`, `*.gcr.io`; others -> `credentials.ErrNoCredential`.
- Credential: user `oauth2accesstoken`, password = access token, `ExpiresAt` = token expiry.
- ADC order: `GOOGLE_APPLICATION_CREDENTIALS` (key or WIF config), gcloud file, metadata server (GKE WI).
- `oauth2.TokenSource.Token` takes no ctx; token HTTP client also carries 30s timeout (HISS-02).
- Missing ADC -> error on first Google-host lookup (never anonymous). ADC discovery runs per lookup under 30s deadline; `credentials.Cache` keeps result per token lifetime.
