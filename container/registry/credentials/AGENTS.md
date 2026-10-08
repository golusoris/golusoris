<!--
SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

SPDX-License-Identifier: CC-BY-SA-4.0
-->

# container/registry/credentials

Registry credential chain behind `container/registry`'s injectable
`authn.Keychain` seam. Workload identity first; no static cloud keys.

## API

```go
type Provider interface {
    Credential(ctx context.Context, host string) (Credential, error)
}
chain := credentials.Chain{static, cloud}                // first match wins
cached := credentials.NewCache(chain, clk, 5*time.Minute)
kc := credentials.Keychain(cached)                        // authn.Keychain + authn.ContextKeychain
kc, err := credentials.NewKeychain(opts, cloudProviders, clk) // static -> cloud (cached) -> authn.DefaultKeychain
c := registry.New(registry.Options{}, kc, nil)
```

- `Credential{Username, Password, RefreshToken, AccessToken, ExpiresAt}` -> `authn.AuthConfig{Username, Password, IdentityToken, RegistryToken}`.
- Host = `authn.Resource.RegistryStr()`: `ghcr.io`, `harbor.example.com:8443`, Docker Hub `index.docker.io`.
- Not serving host -> `ErrNoCredential` -> `authn.Anonymous` -> multi-keychain moves on.
- Any other error stops chain and surfaces. No silent anonymous fallback.
- `Cache`: keeps credentials with `ExpiresAt` until `ExpiresAt - skew`; zero `ExpiresAt` never cached; 256 hosts max.
- Context-less `Resolve` bounded by `ResolveTimeout` (30s); ggcr calls `ResolveContext`.

## Providers

| Provider | Hosts | Source |
| --- | --- | --- |
| `StaticFiles` | configured hosts | secret files re-read per lookup (rotation); 64 KiB cap |
| `ecr` | `<acct>.dkr.ecr[-fips].<region>.amazonaws.com[.cn]`, `.on.aws` | AWS default chain: IRSA, Pod Identity, IMDS -> `GetAuthorizationToken` |
| `gar` | `*-docker.pkg.dev`, `gcr.io`, `*.gcr.io` | ADC: GKE WI metadata, WIF file, gcloud file -> `oauth2accesstoken` + access token |
| `acr` | `*.azurecr.io`, `.cn`, `.us` | azidentity default chain (AKS WI) -> `/oauth2/exchange` -> null-GUID user + refresh token |
| `authn.DefaultKeychain` | entries in config.json | `$DOCKER_CONFIG` / `~/.docker/config.json`, `credsStore`, `credHelpers` |

## fx wiring

```go
fx.New(config.Module, clock.Module,
    credentials.Module, ecr.Module, gar.Module, acr.Module, // cloud modules opt-in
    registry.Module)                                        // consumes optional authn.Keychain
```

Cloud host patterns disjoint, so value-group order irrelevant. Custom
providers: `credentials.ProvideFn(constructor)`.

```yaml
container:
  registry:
    credentials:
      cache_skew: 5m
      static:
        - host: harbor.example.com
          username: robot$vmafx              # or username_file
          password_file: /var/run/secrets/harbor/token
        - host: registry.example.com:5000
          identity_token_file: /var/run/secrets/reg/token
      docker_config:
        disabled: false                      # path via DOCKER_CONFIG env
      acr:
        tenant_id: ""
        timeout: 30s
```

## Dependency choice

- ggcr `pkg/v1/google.Keychain` rejected: falls back to anonymous silently, caches first resolution forever, execs `gcloud`.
- ggcr `k8schain` rejected: pulls client-go for pull-secret lookup only.
- ECR: own thin `GetAuthorizationToken` (aws-sdk-go-v2 `service/ecr`), not `amazon-ecr-credential-helper` (file caches, ecrpublic, helper glue).
- GAR: `golang.org/x/oauth2/google` ADC; `cloud.google.com/go/auth` rejected (gRPC, OTel, google-api deps).
- ACR: azidentity + own exchange; `docker-credential-acr-env` unmaintained since 2023.

## Don't

- Don't return empty `Credential{}` with nil error for unknown host; return `ErrNoCredential`.
- Don't log `Credential` values.
- Don't point tests at real cloud endpoints; `ecr` tests block non-loopback egress.
- Docker `credsStore`/`credHelpers` run `docker-credential-<name>` binaries; set `docker_config.disabled` in hardened pods.
